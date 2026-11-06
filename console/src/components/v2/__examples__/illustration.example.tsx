import { Illustration } from "../illustration";
import { illustrationNames } from "../illustrations";

export function IllustrationExample() {
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {illustrationNames.map((name) => (
          <figure
            key={name}
            className="flex flex-col items-center gap-2 rounded-card border border-line-soft bg-surface-card p-3"
          >
            <Illustration name={name} size="md" />
            <figcaption className="type-data text-ink-soft">{name}</figcaption>
          </figure>
        ))}
      </div>
      <div className="flex flex-wrap items-center justify-around gap-4 rounded-stage bg-gradient-hero p-4 on-gradient">
        {illustrationNames.slice(0, 4).map((name) => (
          <Illustration key={name} name={name} size="sm" className="w-24" />
        ))}
      </div>
    </div>
  );
}
