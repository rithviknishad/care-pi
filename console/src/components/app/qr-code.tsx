import { useMemo } from "react";
import { encode } from "uqr";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// A QR code as an SVG path. The console makes it in the browser: no request leaves the origin.
// The code is always dark on a light square, so a phone can scan it in the dark theme too.
export function QrCode({ value, className, label }: { value: string; className?: string; label?: string }) {
  const { path, size } = useMemo(() => {
    const qr = encode(value, { ecc: "M", border: 2 });
    let d = "";
    qr.data.forEach((row, y) => {
      row.forEach((on, x) => {
        if (on) d += `M${x} ${y}h1v1h-1z`;
      });
    });
    return { path: d, size: qr.size };
  }, [value]);
  return (
    <svg
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={label ?? fmt(strings.qr.label, { value })}
      className={cn("aspect-square rounded-md bg-white text-black", className)}
      shapeRendering="crispEdges"
    >
      <path d={path} fill="currentColor" />
    </svg>
  );
}
