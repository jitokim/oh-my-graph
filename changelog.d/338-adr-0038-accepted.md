### Documented

ADR 0038 (Accepted) settles how `auto` will reuse fragments: the planner cites an engine-admitted fragment from a menu by id, trusted code validates and splices it, the committed path keeps reuse on because admission limits a fragment to what the planner could write itself, and a fragment with a lint advisory is not offered. The first menu is `read-and-report`. ([#338](https://github.com/jitokim/oh-my-graph/issues/338))
