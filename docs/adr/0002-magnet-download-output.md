# A magnet download owns one output

NimGet represents all files in a torrent as one download task. A single-file torrent produces a file, and a multi-file torrent produces a directory reserved for that task. This preserves one set of task actions and gives NimGet a single destination to reserve, reveal, and clean up safely, including for torrents with nested files.

NimGet stages and verifies the payload on the destination volume, then publishes the output with an exclusive move. A durable publication record connects the staged output to the completed task and lets restart recovery finish recording a successful move.
