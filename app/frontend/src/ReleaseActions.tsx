import { Browser } from "@wailsio/runtime";
import { type State } from "./api";
import { useI18n } from "./i18n";

export default function ReleaseActions({ release, busy, onDownload }: {
  release: NonNullable<State["update"]>;
  busy: boolean;
  onDownload: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="release-actions">
      <button type="button" className="small" onClick={() => Browser.OpenURL(release.url)}>
        {t("banner.releaseNotes")}
      </button>
      {release.package && (
        <button type="button" className="primary small" disabled={busy} onClick={onDownload}>
          {t(busy ? "update.queuing" : "update.download")}
        </button>
      )}
    </div>
  );
}
