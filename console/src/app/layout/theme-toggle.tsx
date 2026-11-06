import { ThemeToggle as V2ThemeToggle } from "@/components/v2/theme-toggle";
import { strings } from "@/lib/strings";

// System, light, or dark (the v2 toggle with the texts of the console). next-themes keeps the choice in
// localStorage (care-pi.theme).
export function ThemeToggle({ className }: { className?: string }) {
  return <V2ThemeToggle labels={strings.theme} className={className} />;
}
