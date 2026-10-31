// The handoff of the restore of a new device to the login page: the job deletes each session, so the success text
// shows on the login page (docs/api/README.md section 13.9.3). sessionStorage keeps it for this tab only.
const KEY = "care-pi.dr-restored";

export interface DrHandoff {
  name: string;
  consoleUrl: string | null;
}

export function saveDrHandoff(h: DrHandoff): void {
  try {
    sessionStorage.setItem(KEY, JSON.stringify(h));
  } catch {
    // Private mode: the login page shows no text.
  }
}

export function readDrHandoff(): DrHandoff | null {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<DrHandoff>;
    return typeof v.name === "string" ? { name: v.name, consoleUrl: v.consoleUrl ?? null } : null;
  } catch {
    return null;
  }
}

export function clearDrHandoff(): void {
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    // Nothing to clear.
  }
}
