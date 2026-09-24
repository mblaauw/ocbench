# Sweep three packages

Three independent packages live under `alpha/`, `beta/` and `gamma/`. Each one
declares a retention period for the data it manages, and the values differ.

Use the `task` tool with `subagent_type: explore` to investigate the packages
(one subagent per package is the intended shape), then report the retention
period each package declares. Answer with all three, naming the package and its
value. Do not modify anything.
