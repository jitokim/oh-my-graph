package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Route modes: a chat turn is either answered directly (converse) or handed to
// the planner as a goal (graph).
const (
	RouteConverse = "converse"
	RouteGraph    = "graph"
)

// Route is the router's classification of one chat turn. Exactly one of
// Reply/Goal is meaningful, selected by Mode: RouteConverse carries the text to
// print back to the user, RouteGraph carries the goal to hand to Plan.
type Route struct {
	Mode  string
	Reply string
	Goal  string
}

// routerDecision is the strict JSON shape the router prompt asks for. The
// fields mirror Route, but this stays a separate unmarshal target so the
// router's wire format and the caller-facing value can evolve apart.
type routerDecision struct {
	Mode  string `json:"mode"`
	Reply string `json:"reply"`
	Goal  string `json:"goal"`
}

// Route classifies one chat message with a single call through the same
// NodeRunner seam the planner uses (env-scrubbed, subscription-auth,
// read-only, deny-listed like a node declaring no tools). The reply is parsed
// with the same tolerance as a planner reply — a markdown fence or stray prose
// around the JSON object is fine — and anything that still fails to parse as a
// usable decision falls back to converse, echoing the raw reply, so a sloppy
// router turn degrades to chat instead of killing the loop.
func (c *Coordinator) Route(ctx context.Context, message string) (Route, error) {
	if strings.TrimSpace(message) == "" {
		return Route{}, errors.New("chat router: message is empty")
	}

	outcome, err := c.runner.Run(ctx, coordinatorInvocation(routerPrompt(message)))
	if err != nil {
		return Route{}, fmt.Errorf("router run: %w", err)
	}
	if outcome.ExitCode != 0 {
		return Route{}, fmt.Errorf("chat router exited with code %d\nrouter replied:\n%s",
			outcome.ExitCode, truncate(outcome.Result, maxOutputInError))
	}
	return parseRoute(outcome.Result), nil
}

// parseRoute turns the router's raw reply into a Route. Every malformed shape
// — no JSON object, invalid JSON, an unknown mode, a graph decision with no
// goal — resolves to the converse fallback rather than an error: the reply is
// still text a user can read, and a chat loop that dies on one bad
// classification is worse than one that answers oddly.
func parseRoute(result string) Route {
	spec := extractJSON(result)
	if spec == "" {
		return converseFallback(result)
	}
	var decision routerDecision
	if err := json.Unmarshal([]byte(spec), &decision); err != nil {
		return converseFallback(result)
	}
	switch decision.Mode {
	case RouteConverse:
		return Route{Mode: RouteConverse, Reply: decision.Reply}
	case RouteGraph:
		if strings.TrimSpace(decision.Goal) == "" {
			return converseFallback(result)
		}
		return Route{Mode: RouteGraph, Goal: decision.Goal}
	default:
		return converseFallback(result)
	}
}

// converseFallback wraps a reply the router failed to structure as a plain
// converse turn carrying the raw text.
func converseFallback(result string) Route {
	return Route{Mode: RouteConverse, Reply: strings.TrimSpace(result)}
}

// routerPrompt renders the classification instruction for one chat message.
func routerPrompt(message string) string {
	return fmt.Sprintf(routerPromptTemplate, message)
}

const routerPromptTemplate = `You are the chat router for oh-my-graph, an orchestrator that runs each node
of a DAG as its own claude subprocess. The user typed one message into an
interactive chat. Decide whether it is conversation to answer directly, or a
unit of work oh-my-graph should plan and run as a graph.

User message:

%s

Reply with ONLY a JSON object in exactly one of these two shapes — no markdown
fence, no prose before or after:

{"mode":"converse","reply":"<your direct answer to the message>"}
{"mode":"graph","goal":"<a complete, self-contained goal for the graph planner>"}

Route to "graph" only when the message asks for real work on this machine or
repository (build, test, fix, refactor, write files, commit, ...). Questions,
small talk, and anything ambiguous stay "converse".
`
