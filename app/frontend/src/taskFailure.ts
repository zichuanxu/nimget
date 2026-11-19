import { Failure, SourceKind, type Item } from "./api";

export function taskFailure(item: Item): Failure | "magnet-duplicate" | "magnet-unknown" {
  if (item.sourceKind === SourceKind.SourceMagnet) {
    if (item.duplicateOf) return "magnet-duplicate";
    if (item.failure === Failure.FailureNetwork) return Failure.FailureMagnetPeersStalled;
    return item.failure || "magnet-unknown";
  }
  if (item.failure) return item.failure;

  // Older records only have a diagnostic. Match known messages without displaying it.
  const error = item.error ?? "";
  if (/\bHTTP (401|403)\b/i.test(error)) return Failure.FailureAuthorization;
  if (/not enough free disk space|no space left on device|not enough space on the disk/i.test(error)) return Failure.FailureDiskSpace;
  if (/permission denied|access is denied/i.test(error)) return Failure.FailureFilesystemPermission;
  if (/HTTP (request timed out|server name lookup failed|network connection failed)|connection stalled|unexpected EOF/i.test(error)) return Failure.FailureNetwork;
  if (/unsupported HLS feature|live HLS playlists are not supported/i.test(error)) return Failure.FailureUnsupportedMedia;
  return Failure.$zero;
}
