package serve

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A bind failure says the port is taken and stops there, which leaves the one
// question the reader actually has unanswered: taken BY WHAT. The expensive
// version of that question is the one this repo has already paid for once — a
// long-lived `serve` holding 8642 while `bin/oh-my-graph` is rebuilt underneath
// it reads, from the browser, exactly like the new build misbehaving (Build's
// doc comment). The bind error is where that confusion starts, so it is where
// the answer belongs.
//
// The answer is asked for over HTTP, on the port that just refused the bind,
// and read off the page's own <meta> tags — the machine-readable half of the
// identity a served page already states (build.go). That is deliberately the
// SAME channel the page carries, not a second one invented for this path: a
// diagnostic that could disagree with the page it is diagnosing would be worse
// than no diagnostic.
//
// What this must NOT become is the obvious alternative: `lsof -i :8642` and a
// pid, then `kill`. Exactly four objects in this repo may spawn a process (ADR
// 0002/0005/0006, internal/invariants asserts it by import), and a diagnostic is
// not worth a fifth. An HTTP GET to loopback spawns nothing, and it answers a
// better question than a pid does — not "which process", but "which BUILD". So
// every message below reports, names a command the reader can run, and stops
// there: nothing here kills, replaces, or offers to.

// probeTimeout bounds the WHOLE probe — connect, response, and body read — so
// that a holder which accepts the connection and then never answers costs a
// bind error two seconds of extra latency rather than hanging the command that
// was already failing. Two seconds is generous for a loopback GET (this is the
// same machine) and short enough that a reader waiting on an error does not
// wonder whether it hung.
const probeTimeout = 2 * time.Second

// probeBodyLimit caps how much of the holder's answer is read. The three tags
// live in the page's <head>, within the first kilobyte of both templates; 64 KiB
// is room for a page that grows and a bound against a holder that answers with
// a stream that never ends. A body cut off here is not an error — the tags, if
// there were any, are already past.
const probeBodyLimit = 64 << 10

// holderKind is which of three worlds a failed bind is in. The three are
// exhaustive by construction: either the port answers as an oh-my-graph that
// states its build, and then it either matches this binary or it does not, or
// it does not answer that way at all — which covers a foreign server, a
// `serve` old enough to predate the <meta> tags, and silence alike, because
// none of the three can be told apart from outside and all three want the same
// advice.
type holderKind int

const (
	// holderUnidentified: no answer, an answer that is not an oh-my-graph page,
	// or one that names no version. Nothing can be said about the holder, so
	// nothing is: the error keeps the sentence it has always carried.
	holderUnidentified holderKind = iota
	// holderSameBuild: another oh-my-graph serve, byte-identical build atoms.
	// The reader already has this exact server running.
	holderSameBuild
	// holderOtherBuild: another oh-my-graph serve, different build atoms. This
	// is the case the whole probe exists for — the reader's browser is showing
	// them a build that is not the one they just ran.
	holderOtherBuild
)

// portHolder is a probe's classified answer: which world, and the two builds
// the message names. It is a value with no behavior beyond rendering itself,
// so the classification can be driven against an httptest server on loopback
// and asserted without a bind failure anywhere in sight.
type portHolder struct {
	kind holderKind
	// port is the port that refused the bind — the one that was probed, and the
	// one every message names in both the URL it points at and the --port
	// suggestion it ends with.
	port int
	// holder is what the port's own page says it is: the three <meta> atoms,
	// plus the label rendered from them by build.go's single label author. The
	// zero Build unless kind identified one.
	holder Build
	// self is what this binary is, as build.go established it. The zero Build
	// when the caller did not state one, which is the one case where a holder
	// CAN be identified and still not classified: with nothing to compare
	// against, "older" and "the same" are both unfounded, so the answer degrades
	// to holderUnidentified rather than guessing (probePortHolder).
	self Build
}

// probePortHolder asks the port that just refused a bind who is holding it.
//
// It runs on the bind-failure path ONLY, which is what keeps a successful
// `serve` free of any network call at all: the happy path never reaches here.
//
// Loopback only, and only the port in question: the request goes to
// 127.0.0.1:<port>, the same address Listen just failed to bind, and the
// default client refuses to follow a redirect anywhere else — a holder that
// answers 302 to a host it names cannot turn this diagnostic into an outbound
// request. A nil client gets that default; tests pass their own.
//
// Every failure mode — port 0 (nothing fixed to probe), a refused connection, a
// timeout, a non-200, a page with no tags, a page that names no version — lands
// on the same holderUnidentified answer. The distinctions between them are real
// but none of them changes the advice, and a bind error is the wrong place to
// teach the difference between a foreign server and a silent one.
func probePortHolder(client *http.Client, port int, self Build) portHolder {
	answer := portHolder{port: port, self: self}
	if port <= 0 {
		// Port 0 asked the OS for any free port, so there is no fixed port a
		// holder could be holding: this bind failed for some other reason
		// entirely, and there is nothing to interrogate.
		return answer
	}
	if client == nil {
		client = defaultProbeClient()
	}

	// The deadline is on the context as well as (usually) on the client, so a
	// caller-supplied client without a Timeout is still bounded, and the bound
	// covers the body read below rather than just the response headers.
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(port), nil)
	if err != nil {
		return answer
	}
	resp, err := client.Do(req)
	if err != nil {
		return answer
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return answer
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
	if err != nil {
		return answer
	}

	holder, ok := holderBuild(string(page))
	if !ok {
		return answer
	}
	answer.holder = holder
	if self.Version == "" {
		// A holder was identified, but this binary was handed no identity to
		// compare it with (the zero Build — the embedded live view, or any
		// caller that does not state one). Naming it "older" or "the same"
		// would be an invention; the reader gets the --port sentence instead.
		return answer
	}
	if holder.Version == self.Version && holder.Revision == self.Revision && holder.BuiltAt == self.BuiltAt {
		answer.kind = holderSameBuild
		return answer
	}
	answer.kind = holderOtherBuild
	return answer
}

// probeURL is the one address this package ever probes: the loopback port that
// refused the bind, at its root, which is where both front-ends serve the page
// carrying the tags (the dashboard's index and a single run's view alike).
func probeURL(port int) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/"
}

// defaultProbeClient is the client the bind path uses: bounded in time, and
// refusing to follow a redirect at all. The redirect rule is not paranoia about
// oh-my-graph, which never redirects — it is about everything else that might
// be holding the port, since following a redirect would let whatever answers
// 127.0.0.1 choose the next host this process talks to.
func defaultProbeClient() *http.Client {
	return &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// buildMetaPattern matches the three build <meta> tags both templates emit, in
// the exact attribute order they emit them (name before content) — the same
// anchoring build_test.go's metaContent uses, and for the same reason: a
// template that reordered the attributes should stop matching loudly rather
// than keep half-matching quietly.
var buildMetaPattern = regexp.MustCompile(`<meta name="(omg-version|omg-revision|omg-built-at)" content="([^"]*)">`)

// holderBuild reads a probed page's build atoms off its <meta> tags.
//
// ok is false unless all three tags are present AND the version is non-empty,
// which is exactly the identity claim this can act on. The three-tag rule is
// the contract build_test.go pins from the other side: an unknown atom renders
// as an EMPTY tag, never an omitted one, so a missing tag means a server that
// predates the tags — identifiable as oh-my-graph by nothing here, and rightly
// left to the --port sentence.
//
// The content is unescaped because html/template escapes an RFC3339 offset's
// '+' as "&#43;" in attribute context; comparing that against this process's
// own BuiltAt without unescaping would call every build different from itself.
func holderBuild(page string) (Build, bool) {
	found := make(map[string]string, 3)
	for _, match := range buildMetaPattern.FindAllStringSubmatch(page, -1) {
		found[match[1]] = html.UnescapeString(match[2])
	}
	version, hasVersion := found["omg-version"]
	revision, hasRevision := found["omg-revision"]
	builtAt, hasBuiltAt := found["omg-built-at"]
	if !hasVersion || !hasRevision || !hasBuiltAt || version == "" {
		return Build{}, false
	}
	// Whatever is holding that port is not necessarily ours, and its answer is
	// the only untrusted input on this path — it goes straight into a message a
	// terminal renders. An atom carrying control bytes is therefore not an
	// identity this can act on: `\r` and `\x1b[2J` let a holder erase the
	// diagnostic and print its own, and "no other serve is running" is the
	// sentence it would choose. Our own templates emit a version string, a VCS
	// revision and an RFC3339 instant, none of which can contain one, so
	// refusing is exact rather than defensive — a real oh-my-graph page loses
	// nothing, and an answer that is trying to be a terminal escape is treated
	// as what it is: not an oh-my-graph.
	if !plainAtom(version) || !plainAtom(revision) || !plainAtom(builtAt) {
		return Build{}, false
	}
	return Build{
		Version:  version,
		Revision: revision,
		BuiltAt:  builtAt,
		// Rendered by build.go's labelFor, the same author that labels this
		// process's own build — so the two builds a message names are named in
		// one voice, and a change to how a build reads changes both.
		Label: labelFor(version, revision, builtAt),
	}, true
}

// atomLimit caps how long a single build atom may be before this stops calling
// it an identity. Our own three are a semver, a short revision and an RFC3339
// instant — none near this — so the cap costs a real page nothing and denies a
// holder the ability to answer with a kilobyte that scrolls the actual error off
// the reader's screen. The body limit above bounds the transfer; this bounds
// what reaches the terminal.
const atomLimit = 128

// plainAtom reports whether a probed build atom is safe to render into an error
// a terminal will interpret: printable, single-line, and bounded. It is the
// whole trust boundary for the probe's answer — everything downstream of
// holderBuild treats a Build as this process's own.
func plainAtom(atom string) bool {
	if len(atom) > atomLimit {
		return false
	}
	for _, r := range atom {
		// Unicode's C0/C1 control ranges, which is where every terminal escape
		// and every line break lives. unicode.IsControl covers both, and DEL.
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// message renders the classified answer as the tail of Listen's bind error —
// everything after "listen on 127.0.0.1:8642: bind: address already in use".
//
// The unidentified world returns the sentence that error has always carried,
// byte for byte: nothing was learned, so nothing new is said. The other two
// name what was learned and then name a command the reader can run. None of
// them offers to stop, kill or replace the holder, and none of them can: this
// process knows a build, not a pid, and that is the whole shape of the feature.
func (h portHolder) message() string {
	switch h.kind {
	case holderSameBuild:
		return fmt.Sprintf("— 127.0.0.1:%d is already held by another `oh-my-graph serve` running this same build, %s: "+
			"you already have one running, and it is answering at %s. "+
			"To serve a second one beside it, run `oh-my-graph serve --port %d`.",
			h.port, h.holder.Label, probeURL(h.port), nextFreePortSuggestion(h.port))
	case holderOtherBuild:
		return fmt.Sprintf("— 127.0.0.1:%d is held by another `oh-my-graph serve`, and it is %s build than the binary you just ran: "+
			"the server answering there is %s, this binary is %s. "+
			"What you see at %s is %s, not the code you just built; nothing here stops it. "+
			"To serve this build beside it, run `oh-my-graph serve --port %d`.",
			h.port, h.relation(), h.holder.Label, h.self.Label, probeURL(h.port), h.holderNoun(), nextFreePortSuggestion(h.port))
	default:
		return "(if the port is taken, pick another with --port)"
	}
}

// relation is how the holder's build stands to this one — "an older", "a newer"
// or "a different" — decided by the two BuiltAt atoms, which are RFC3339
// precisely so they order.
//
// It says "different" whenever the two cannot be ordered (either atom absent or
// unparseable, or the same instant with different atoms elsewhere). The stale
// holder is the case this feature was reported for, so "older" is the sentence
// it will almost always print — but printing "older" at a holder that is in fact
// NEWER would be a confident wrong answer about the exact thing the reader came
// here to learn, and this diagnostic exists because a confident wrong impression
// cost someone a debugging session already.
func (h portHolder) relation() string {
	holderAt, holderErr := time.Parse(time.RFC3339, h.holder.BuiltAt)
	selfAt, selfErr := time.Parse(time.RFC3339, h.self.BuiltAt)
	switch {
	case holderErr != nil || selfErr != nil:
		return "a different"
	case holderAt.Before(selfAt):
		return "an older"
	case holderAt.After(selfAt):
		return "a newer"
	default:
		return "a different"
	}
}

// holderNoun names the holder's build the second time the message refers to it,
// agreeing with relation() so the sentence reads as one thought rather than two
// ("an older build … that older build").
func (h portHolder) holderNoun() string {
	return "that " + strings.TrimPrefix(strings.TrimPrefix(h.relation(), "an "), "a ") + " build"
}

// nextFreePortSuggestion is the port the messages tell the reader to try. It is
// the next one up — the smallest change to the command they just ran — falling
// back to the default when there is no next port to name.
//
// It is a suggestion and not a promise: nothing here binds it to check, since
// this process is on its way out with an error either way, and probing a second
// port to advertise it would be a second thing that can hang.
func nextFreePortSuggestion(port int) int {
	if port > 0 && port < 65535 {
		return port + 1
	}
	return DefaultPort
}
