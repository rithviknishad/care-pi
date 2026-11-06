import { CopyButton } from "../copy-button";
import { MonoBlock } from "../mono-block";

const FINGERPRINT = "21:69:AE:1D:DD:1B:4E:76:C0:E5:35:CA:0F:86:B1:2D:16:72:8C:4D:A6:54:E8:AB:00:14:28:AD:7F:38:99";
const LOG = [
  "09:41:02 care-api  Starting gunicorn 23.0.0",
  "09:41:03 care-api  Listening at: http://0.0.0.0:9000",
  "09:41:05 care-api  Booting worker with pid: 31",
  "09:41:05 care-api  Booting worker with pid: 32",
  "09:41:09 care-api  GET /api/v1/health 200 3ms",
].join("\n");

export function CopyExample() {
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap gap-2">
          <CopyButton text={FINGERPRINT} label="Copy the fingerprint" />
          <CopyButton text={LOG} report title="Log of care-api" label="Copy for support" />
          <CopyButton text={FINGERPRINT} iconOnly />
        </div>
        <MonoBlock label="Fingerprint (SHA-256)" text={FINGERPRINT} />
      </div>
      <MonoBlock label="Log of care-api" text={LOG} mode="pre" lines={6} copy="report" copyTitle="Log of care-api" />
    </div>
  );
}
