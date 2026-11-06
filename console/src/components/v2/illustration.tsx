import { cn } from "@/lib/utils";
import { illustrations, type IllustrationName } from "./illustrations";

const SIZES = {
  sm: "w-20", // 80 x 64
  md: "w-32", // 128 x 102
  lg: "w-48", // 192 x 154
  xl: "w-60", // 240 x 192
} as const;

export interface IllustrationProps {
  name: IllustrationName;
  size?: keyof typeof SIZES;
  /** A label makes the illustration an image for screen readers. Without it, it is decoration. */
  label?: string;
  className?: string;
}

// An inline SVG (2-colour line art). The box has a fixed width and the 5:4 ratio of the art, so it
// reserves its space before the SVG paints.
export function Illustration({ name, size = "md", label, className }: IllustrationProps) {
  return (
    <span
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      data-illustration={name}
      className={cn("inline-block aspect-[5/4] shrink-0 [&>svg]:block [&>svg]:size-full", SIZES[size], className)}
      dangerouslySetInnerHTML={{ __html: illustrations[name] }}
    />
  );
}
