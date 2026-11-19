# NimGet owns magnet downloads

NimGet ships and controls the BitTorrent runtime as part of the application and headless service. Magnet downloads share the existing download queue, and NimGet owns their execution, pause, resume, retry, and restart recovery. This gives users one queue to manage and lets NimGet apply its own scheduling and completion rules to every download task.
