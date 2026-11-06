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
export async function writeClipboard(text: string): Promise<boolean> {
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
