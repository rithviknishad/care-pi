import { ProgressRing } from "../progress-ring";

export function ProgressRingExample() {
  return (
    <div className="flex flex-wrap items-center gap-6">
      <ProgressRing value={0.25} label="Step 3 of 8">
        25%
      </ProgressRing>
      <ProgressRing value={0.7} size={96} stroke={8} label="Download">
        70%
      </ProgressRing>
      <ProgressRing value={null} size={40} stroke={4} label="Running" />
      <div className="grid place-items-center rounded-stage bg-gradient-hero p-4 on-gradient">
        <ProgressRing value={0.5} onGradient label="Half done">
          50%
        </ProgressRing>
      </div>
    </div>
  );
}
