import { createContext, useContext } from "react";
import type { CopyHeader } from "./copy";

// The header of each report copy. The app shell gives it once (CopyHeaderProvider), so each CopyButton
// in a page writes the same device name and agent version.
export const CopyHeaderContext = createContext<CopyHeader | null>(null);

export function useCopyHeader(): CopyHeader | null {
  return useContext(CopyHeaderContext);
}
