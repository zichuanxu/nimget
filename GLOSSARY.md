# NimGet

NimGet manages file downloads and their handoff from a browser to the desktop application.

## Language

**Download task**:
A user's request to retrieve a resource to a local destination, including its progress and lifecycle state.
_Avoid_: Connection, segment

**Magnet download**:
A download task created from a magnet link. The link identifies the BitTorrent content, and its torrent metadata describes the files.

**Torrent identity**:
The identity of one torrent, represented by its full v1 or v2 info hash. A verified hybrid torrent has both hashes as equivalent identifiers.

**Download output**:
The local file or directory produced by a download task. A multi-file magnet download has one output directory containing its files.

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
