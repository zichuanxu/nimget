import { useState } from "react";
import type { State } from "./api";
import { useI18n } from "./i18n";
import ReleaseActions from "./ReleaseActions";

export type UpdateCheck =
  | { status: "idle" | "checking" | "failed" }
  | { status: "checked"; release: State["update"] };

type Props = {
  state: State;
  check: UpdateCheck;
  showTitle?: boolean;
  queuing: boolean;
  onCheck: () => void;
  onDownload: (version: string) => Promise<void>;
  onClose: () => void;
};

export default function UpdateControls({ state, check, showTitle = false, queuing, onCheck, onDownload, onClose }: Props) {
  const { t } = useI18n();
  const [downloadFailed, setDownloadFailed] = useState(false);
  const checking = check.status === "checking";
  const release = check.status === "checked" ? check.release : state.update;

  async function download(version: string) {
    setDownloadFailed(false);
    try {
      await onDownload(version);
      onClose();
    } catch {
      setDownloadFailed(true);
    }
  }

  return (
    <>
      <div className="set-row">
        <div className="set-text">
          {showTitle && <span className="set-label">{t("update.title")}</span>}
          {checking && <span className="hint">{t("update.checking")}</span>}
          <span className="hint" role="status">
            {checking ? "" :
              release ? t("banner.update", { version: release.version }) :
              check.status === "checked" ? t("update.current") :
              !state.canCheckUpdates ? t("update.releaseBuilds") : ""}
          </span>
        </div>
        <div className={showTitle ? "set-control" : undefined}>
          <button type="button" className="update-check" disabled={!state.canCheckUpdates} aria-disabled={checking || undefined} onClick={() => {
            if (checking) return;
            setDownloadFailed(false);
            onCheck();
          }}>
            {t("update.checkNow")}
          </button>
        </div>
      </div>
      {check.status === "failed" && <p className="form-error" role="alert">{t("update.checkFailed")}</p>}
      {downloadFailed && <p className="form-error" role="alert">{t("update.downloadFailed")}</p>}
      {release && !checking && (
        <div className="settings-release">
          {!release.package && <p className="hint">{t("update.noPackage")}</p>}
          <ReleaseActions release={release} busy={queuing} onDownload={() => void download(release.version)} />
        </div>
      )}
    </>
  );
}
