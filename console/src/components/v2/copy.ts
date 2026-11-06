import { v2Strings } from "./strings";

// S68: each log view, job view, problem, and doctor result has "Copy". The text has a header
// (device name, agent version, time), so support knows where it comes from.
export interface CopyHeader {
  device?: string | null;
  version?: string | null;
  page?: string | null;
}

export function copyHeaderLines(header: CopyHeader, now: Date = new Date()): string[] {
  const s = v2Strings.copyHeader;
  const lines = [
    `${s.device}: ${header.device || "—"}`,
    `${s.version}: ${header.version || "—"}`,
    `${s.time}: ${now.toISOString()}`,
  ];
  if (header.page) lines.push(`${s.page}: ${header.page}`);
  return lines;
}

// The text that a report copy writes: the header, an empty line, the title (if any), and the body.
export function buildCopyText(body: string, header?: CopyHeader | null, title?: string, now?: Date): string {
  const parts: string[] = [];
  if (header) parts.push(copyHeaderLines(header, now).join("\n"));
  if (title) parts.push(title);
  parts.push(body);
  return parts.join("\n\n");
}

// Writes the text to the clipboard. Without the Clipboard API (an http:// page), it uses a hidden text area.
// A promise (a text that the console reads from the agent at the click, for example GET /jobs/{id}/export) goes
// into a ClipboardItem first: Safari keeps the user activation only for a write that starts in the click.
export async function writeClipboard(value: string | Promise<string>): Promise<boolean> {
  if (typeof value !== "string") {
    const pending = value;
    try {
      if (typeof ClipboardItem !== "undefined" && navigator.clipboard?.write) {
        const blob = pending.then((t) => new Blob([t], { type: "text/plain" }));
        await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
        return true;
      }
    } catch {
      // Fall back to the text after it comes.
    }
    try {
      value = await pending;
    } catch {
      return false;
    }
  }
  const text = value;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall back to the text area below.
  }
  try {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    area.style.inset = "0 auto auto 0";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    area.remove();
    return ok;
  } catch {
    return false;
  }
}
