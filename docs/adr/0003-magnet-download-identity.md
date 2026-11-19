# An unfinished magnet download is unique per torrent

NimGet returns the existing unfinished task when the user adds the same torrent again. The full v1 and v2 info hashes are equivalent identifiers once verified as aliases of a hybrid torrent. This preserves one task's destination and resume state as links with different display names, discovery hints, or verified hash forms reach the same queue.
