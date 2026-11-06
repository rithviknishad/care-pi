import type { ReactNode } from "react";
import type { CopyHeader } from "./copy";
import { CopyHeaderContext } from "./copy-context";

export function CopyHeaderProvider({ value, children }: { value: CopyHeader; children: ReactNode }) {
  return <CopyHeaderContext.Provider value={value}>{children}</CopyHeaderContext.Provider>;
}
