# Execute user-authored SQL directly

SQL console users already see and edit the exact SQL they intend to run. A
separate preview step duplicates the editor and makes routine execution noisy.
The maintainer chose direct execution to keep the workspace minimal.

Execute and Ctrl/Cmd+Enter send the current script, row limit and execution mode
to one endpoint. Remove the script preview endpoint, token requirement, expiry
state and preview UI. The server still validates every request, enforces read-only
profiles, execution modes, result bounds and the timeout, and records accepted
executions in history. Partial execution keeps its visible warning. Selecting a
history entry only loads the script; execution remains an explicit action.

This amends the original SQL console decision and the project instructions.
Generated grid ChangeSets still require SQL preview before application because
the application generates those statements on the user's behalf.
