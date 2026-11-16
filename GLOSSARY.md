# NimGet

NimGet manages file downloads and their handoff from a browser to the desktop application.

## Language

**Download task**:
A user's request to retrieve a resource to a local destination, including its progress and lifecycle state.
_Avoid_: Connection, segment

**Website session**:
The user's existing authenticated session with the website supplying a download. NimGet itself does not require an account or login.
_Avoid_: NimGet login, NimGet account

**Extension pairing**:
The user's revocable authorization for a browser extension to communicate with their local NimGet application.
_Avoid_: Login, website session

**Manual handoff**:
A download explicitly sent to NimGet by the user through a browser action.
_Avoid_: Automatic capture

**Automatic capture**:
An optional browser behavior that sends eligible downloads to NimGet according to the user's exclusions. Browser downloading is stopped only after NimGet durably accepts the task.
_Avoid_: Manual handoff
