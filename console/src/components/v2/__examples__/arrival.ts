import { createContext, useContext, useEffect, useState } from "react";

// "Data arrival" for the stability checks: each example shows its loading state first, then its data.
// ?arrive=manual waits for window.__v2Arrive() (the Playwright bbox check); else the data comes after 700 ms.
export const ArrivalContext = createContext(true);

export function useArrived(): boolean {
  return useContext(ArrivalContext);
}

declare global {
  interface Window {
    __v2Arrive?: () => void;
  }
}

export function useArrivalSwitch(mode: string | null): boolean {
  const [arrived, setArrived] = useState(false);
  useEffect(() => {
    window.__v2Arrive = () => setArrived(true);
    if (mode === "manual") return;
    const t = setTimeout(() => setArrived(true), mode === "now" ? 0 : 700);
    return () => clearTimeout(t);
  }, [mode]);
  return arrived;
}
