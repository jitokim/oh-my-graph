package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// Dashboard is `oh-my-graph serve` with no run id: ONE port and one page for
// every run, instead of one `serve` process, port and tab per run.
//
// It is a second front-end onto the same run directories, not a second reader
// of them. `/` renders one live mini-DAG card per run, derived by buildCard
// through the existing readers; `/run/<id>/` mounts the unchanged single-run
// view (Server.routes) behind that path, so clicking a card opens exactly the
// page `serve <id>` serves — same handlers, same page, same gate buttons.
//
// It subscribes to the runs root the way a single run subscribes to its
// events.jsonl: /api/cards/events polls the root at the same cadence
// handleEvents tails a stream at (defaultPoll), and streams a card frame for
// every run that is new or has changed since the last sweep. "Changed" is the
// modtime-and-size of the two contract files, so a quiet dashboard costs two
// stats per run per tick and re-reads nothing.
//
// SECURITY: the run id in /run/<id>/ is URL input and is treated as such. It
// is matched against the runs root's DIRECTORY LISTING (runInRoot) before any
// path is built from it, so no id that is not literally the name of a run
// directory ever reaches the filesystem — the same rule handleResult applies
// to node ids, one level up. Everything else the single-run view guards
// (loopback bind, Host check, same-origin check, gate token) is unchanged and
// applies here identically: requireLoopbackHost wraps the whole mux, mounted
// runs included, and each mounted run's gate POSTs keep their own
// requireSameOrigin + requireGateToken pair. securityHeaders wraps the whole
// mux too, so the dashboard and every run mounted under it refuse to be framed
// — this front-end especially, because its "/" needs no run id to address.
type Dashboard struct {
	runsRoot string
	poll     time.Duration
	// projectsRoot is handed to every mounted run's Server, so a test can point
	// the transcript route at a fixture directory exactly as it can for a
	// standalone Server.
	projectsRoot string
	// token is the process's gate token, minted once and SHARED by every run
	// mounted under this dashboard: it identifies the serving process, not the
	// run being decided, and every mounted page is served by this process.
	token string
	// resumer is what makes a mounted run's gate buttons work. Injected by the
	// CLI exactly as for a standalone Server; nil leaves every gate decision a
	// 409. The interface already takes the run id per call, so one resumer
	// serves every run on the dashboard.
	resumer GateResumer
	// build names the binary serving these pages, handed down to every mounted
	// run's Server so the dashboard and the run view it links to can never
	// disagree about which process is answering. Empty renders nothing.
	build string
	// lister names the runs a sweep considers. It is listRunIDs for every real
	// dashboard, and exists as a field for exactly one reason: a run deleted
	// BETWEEN the listing and its stamp is a race a test cannot win by racing,
	// and it is the case handleCardEvents' runDirGone check is there for. A
	// fake lister names a run id whose directory is not there, which is that
	// state with the timing taken out of it.
	lister func(root string) ([]string, error)
}

// dashboardTemplate is the dashboard page. Like the single-run page it is
// rendered rather than shipped byte-for-byte, because it carries the gate
// token forward to the run views opened from it in the same tab.
var dashboardTemplate = template.Must(template.ParseFS(uiFS, "ui/dashboard.html"))

// NewDashboard builds a dashboard over a runs root. The root need not exist:
// a machine that has never run anything gets an empty dashboard that fills in
// as soon as something runs, which is the honest answer and the useful one —
// `serve` on a fresh checkout should not be an error page.
func NewDashboard(runsRoot string) *Dashboard {
	return &Dashboard{
		runsRoot:     runsRoot,
		poll:         defaultPoll,
		projectsRoot: defaultProjectsRoot(),
		token:        newGateToken(),
		lister:       listRunIDs,
	}
}

// WithGateResumer injects the resume machinery every mounted run's gate routes
// need, and returns the Dashboard so it chains onto NewDashboard — the mirror
// of Server.WithGateResumer, for the same reason and with the same effect.
func (d *Dashboard) WithGateResumer(r GateResumer) *Dashboard {
	d.resumer = r
	return d
}

// Handler returns the dashboard's routes:
//
//	/                   the dashboard page: one mini-DAG card per run
//	/api/cards          every run's card as JSON, newest first
//	/api/cards/events   card updates as SSE: one frame per new or changed run
//	/run/<id>/...       the single-run live view, unchanged, mounted per run
//	/style.css, /dashboard.css, /dashboard.js  the three assets its page loads
//
// The single-run view keeps its whole route set under its mount, so a card
// click lands on /run/<id>/ and everything that page fetches — api/graph,
// api/events, api/result, api/transcript, the gate POSTs, app.js, style.css,
// the vendored libraries — resolves under the same prefix without the page
// knowing it moved.
//
// Every route sits behind requireLoopbackHost, the DNS-rebinding guard, and
// securityHeaders, the frame/CSP guard, once for the whole mux — the mounted
// runs included.
func (d *Dashboard) Handler() http.Handler {
	static, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// Unreachable: "ui" is embedded at compile time.
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.handleIndex)
	mux.HandleFunc("GET /api/cards", d.handleCards)
	mux.HandleFunc("GET /api/cards/events", d.handleCardEvents)
	// Named one by one rather than served as a subtree: these three ARE the
	// dashboard page, and style.css is deliberately among them — it is the
	// shared token and chip/dot source of truth both pages load, which is what
	// keeps the two from drifting on colour.
	for _, asset := range []string{"/style.css", "/dashboard.css", "/dashboard.js"} {
		mux.Handle("GET "+asset, http.FileServerFS(static))
	}
	// No method here, and a subtree pattern: a mounted run answers on every
	// route its own mux declares, gate POSTs included, and ServeMux's own
	// trailing-slash redirect turns /run/<id> into /run/<id>/ — which matters,
	// because the page's relative URLs are resolved against that slash.
	mux.HandleFunc("/run/{run}/", d.handleRun)
	// The dashboard deliberately does NOT serve the run view's own assets at
	// the root: there is no run at "/", so app.js there would poll a run that
	// does not exist. Anything else is a 404.
	return securityHeaders(requireLoopbackHost(mux))
}

// WithBuild sets the build label this dashboard and every run mounted under it
// state in their footers — the mirror of Server.WithBuild, injected by the CLI
// for the same reason.
func (d *Dashboard) WithBuild(label string) *Dashboard {
	d.build = label
	return d
}

// handleIndex serves the dashboard page with this process's gate token, which
// the run views opened from it inherit through their own handleIndex — the
// token is the process's, so the two pages carry the same one. The build label
// is the process's too, and travels the same way.
func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	var page bytes.Buffer
	if err := dashboardTemplate.Execute(&page, struct{ Token, Build string }{d.token, d.build}); err != nil {
		http.Error(w, fmt.Sprintf("render page: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Bytes())
}

// handleRun mounts one run's live view under /run/<id>/.
//
// SECURITY: the membership guard is the first thing that happens, and it is a
// LISTING match, not a stat of a joined path — the id from the URL is compared
// against the names of the directories under the runs root, and only a literal
// match proceeds. A run id is a directory name, and a directory name never
// contains a separator, so a traversal probe cannot match one; an id that does
// not name a run (a typo and an attack alike) is a 404 that has touched
// nothing but the root's listing.
func (d *Dashboard) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run")
	member, err := runInRoot(d.runsRoot, runID)
	if err != nil {
		http.Error(w, fmt.Sprintf("read runs dir: %v", err), http.StatusInternalServerError)
		return
	}
	if !member {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/run/"+runID, d.serverFor(runID).routes()).ServeHTTP(w, r)
}

// serverFor builds the single-run view for one run of this dashboard. It is
// the ordinary Server every other caller gets, with the three things that
// belong to the serving PROCESS rather than to the run handed down: the poll
// cadence, the transcript root, the gate token and the build label — plus the
// injected resumer, whose interface already names the run per call.
//
// Built per request rather than cached: a Server holds no state about its run
// (every request re-reads the contract files), so a fresh one and a kept one
// answer identically, and there is no cache to invalidate when a run
// directory appears or is deleted.
func (d *Dashboard) serverFor(runID string) *Server {
	s := New(filepath.Join(d.runsRoot, runID), runID)
	s.poll = d.poll
	s.projectsRoot = d.projectsRoot
	s.token = d.token
	s.build = d.build
	s.resumer = d.resumer
	return s
}

// handleCards serves every run's card as one JSON array, newest first. It is
// the same data /api/cards/events streams, in one read — what a card list is
// without a subscription, and what a test asserts the derivation against.
func (d *Dashboard) handleCards(w http.ResponseWriter, r *http.Request) {
	runIDs, err := d.listRuns()
	if err != nil {
		http.Error(w, fmt.Sprintf("read runs dir: %v", err), http.StatusInternalServerError)
		return
	}
	cards := make([]runCard, 0, len(runIDs))
	for _, runID := range runIDs {
		cards = append(cards, buildCard(d.runsRoot, runID))
	}
	writeJSON(w, cards)
}

// handleCardEvents streams card updates as Server-Sent Events. It is the
// dashboard's equivalent of handleEvents: where a single run subscribes to its
// own events.jsonl, the dashboard subscribes to the runs ROOT, at the same
// cadence and with the same never-ends-on-its-own posture.
//
// Every sweep lists the root and stamps each run's two contract files. A run
// that is new, or whose stamp changed, is rebuilt and sent as one `card`
// frame; a run whose directory is gone is sent as one `card_removed` frame. A
// `cards_ready` frame follows the first sweep, so the page can tell "still
// connecting" from "genuinely no runs yet". The client keys frames by run id
// and replaces, so a reconnect rebuilds the whole dashboard deterministically
// — the same replay property the single-run feed has.
//
// A sweep is several observations of a tree that changes under it — the listing
// that decides membership, then the per-run stamp that decides content — so a
// run deleted mid-sweep is a real case, not a corner one, and it is handled
// where the stamp is taken rather than left to the next tick. What makes it
// worth the care is that the intermediate frame is not merely early: a card for
// a deleted run is a tile the page offers a click on, and that click is a 404.
//
// One window is deliberately left open, because closing it would close a
// legitimate case with it. A non-atomic delete (`rm -rf` unlinks the contract
// files, THEN the directory) has an instant where the directory is real and its
// files are not, and a run in that instant is byte-for-byte a healthy young one:
// a directory that has said nothing yet, or a stream whose first node has not
// finished — the shapes
// TestDashboard_ARunDirectoryThatHasSaidNothingIsPendingNotFailed and
// TestDashboard_ARunWithNoSnapshotYetStillGetsALiveCard pin as cards this
// dashboard MUST draw. A run caught there gets one such card, and card_removed
// on the following tick.
//
// The seen-set is per CONNECTION, deliberately: this handler holds no state
// across viewers. A run removed while a viewer was disconnected therefore gets
// no card_removed frame — the next connection's replay simply never names it,
// and the page treats that replay (closed by cards_ready) as the full list.
//
// The stream ends when the client disconnects, never on its own: runs keep
// starting, and a dashboard that stopped listening after the last one settled
// would be exactly the thing this replaces.
func (d *Dashboard) handleCardEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	seen := map[string]runStamp{}
	for first := true; ; first = false {
		runIDs, err := d.listRuns()
		if err != nil {
			sendSSE(w, flusher, "stream_error", errorFrame(err.Error()))
			return
		}
		present := make(map[string]bool, len(runIDs))
		for _, runID := range runIDs {
			present[runID] = true
			stamp := stampRun(d.runsRoot, runID)
			// The listing and the stamp just taken are two observations of a
			// tree that changes under us, and a run deleted between them stamps
			// as empty — which without this check is announced as a CHANGED
			// card: a tile, and a link to a 404, for a run that is already gone,
			// with card_removed a whole tick behind it. Confirming the directory
			// AFTER the stamp settles which of the two an empty stamp was. Still
			// there means the stamp is honest (a young run that has not written
			// its contract files yet, which gets its card); gone means the
			// listing was stale, so this run is not present in this sweep at all
			// and the removal pass below speaks for it immediately.
			//
			// It is confirmed BEFORE the unchanged-stamp fast path, not after,
			// because an empty stamp is also what a run that has not written its
			// contract files yet stamps as: deleting such a run leaves the stamp
			// equal to the one already seen, and a check behind that comparison
			// would never run for exactly the run it was written for.
			if runDirGone(d.runsRoot, runID) {
				delete(present, runID)
				continue
			}
			if prev, known := seen[runID]; known && prev == stamp {
				continue
			}
			seen[runID] = stamp
			frame, err := json.Marshal(buildCard(d.runsRoot, runID))
			if err != nil {
				// Unreachable for a runCard, whose fields are all encodable;
				// skipping beats tearing down a live dashboard for one run.
				continue
			}
			sendSSE(w, flusher, "card", string(frame))
		}
		for runID := range seen {
			if !present[runID] {
				delete(seen, runID)
				sendSSE(w, flusher, "card_removed", removalFrame(runID))
			}
		}
		if first {
			sendSSE(w, flusher, "cards_ready", "{}")
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(d.poll):
		}
	}
}

// removalFrame renders a card_removed payload through the JSON encoder, for
// the same escaping-exactness reason errorFrame does.
func removalFrame(runID string) string {
	b, _ := json.Marshal(struct {
		RunID string `json:"run_id"`
	}{runID})
	return string(b)
}

// runStamp is the cheap change signal for one run directory: the size and
// modtime of the two files a card is derived from. Comparable by ==, which is
// the whole point — a sweep re-reads a run only when its stamp moved.
//
// It is deliberately not a content hash: the run feed is append-only and the
// snapshot is rewritten atomically after every node, so size-and-modtime moves
// exactly when a card's inputs do. A same-size same-modtime rewrite would be
// missed; nothing in the run-feed contract produces one.
type runStamp struct {
	feed  fileStamp
	state fileStamp
}

// fileStamp is one file's size and modtime, with exists distinguishing "not
// written yet" from a zero-length file.
type fileStamp struct {
	exists   bool
	size     int64
	modNanos int64
}

// stampRun stats a run's two contract files. A file that cannot be stat'd at
// all reads as absent, which makes the next sweep rebuild the card and let
// buildCard report the problem on the card itself.
func stampRun(runsRoot, runID string) runStamp {
	runDir := filepath.Join(runsRoot, runID)
	return runStamp{
		feed:  stampFile(filepath.Join(runDir, runfeed.FileName)),
		state: stampFile(filepath.Join(runDir, stateFileName)),
	}
}

// runDirGone reports whether a run directory the sweep listed has vanished
// since it was listed. One stat per listed run per tick, on top of the two
// stampRun takes — the price of the check reaching a run whose stamp did not
// move, which is the very run it exists for.
//
// Only a NOT-EXIST answer counts as gone: a directory that cannot be stat'd for
// any other reason is still a run, and the card buildCard derives for it is how
// that problem reaches the operator.
func runDirGone(runsRoot, runID string) bool {
	_, err := os.Stat(filepath.Join(runsRoot, runID))
	return errors.Is(err, fs.ErrNotExist)
}

func stampFile(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: info.Size(), modNanos: info.ModTime().UnixNano()}
}

// listRuns names the runs this dashboard's card routes consider. Both of them
// go through it, so an injected lister is seen by the whole card front-end
// rather than by one handler.
//
// runInRoot deliberately does NOT: it is the membership guard behind
// /run/<id>/, and a security guard answers from the real directory listing,
// never from an injected one.
func (d *Dashboard) listRuns() ([]string, error) {
	return d.lister(d.runsRoot)
}

// listRunIDs names every run directory under root, newest first. Run ids are
// UTC timestamps chosen to sort lexically (newRunID in cmd/oh-my-graph), so a
// descending string sort is a descending time sort — the same ordering `runs
// list` and ResolveRun use. A root that does not exist yet is no runs, not an
// error: nothing has run here.
func listRunIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read runs dir %q: %w", root, err)
	}
	var runIDs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runIDs = append(runIDs, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runIDs)))
	return runIDs, nil
}

// runInRoot reports whether id names a run directory under root.
//
// SECURITY: this is the membership guard behind /run/<id>/, and it is written
// as a LISTING match on purpose — id is compared against the names os.ReadDir
// reports, and is never joined into a path to be stat'd. Joining first and
// checking after is how "../../etc" becomes a real path; comparing against the
// listing means only a literal directory name can ever match, and a directory
// name cannot contain a separator. Same rule, one level up, as handleResult's
// node-id check.
func runInRoot(root, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read runs dir %q: %w", root, err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == id {
			return true, nil
		}
	}
	return false, nil
}
