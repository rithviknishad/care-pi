import { LaptopIcon, MoonIcon, SunIcon } from "lucide-react";
import { useTheme } from "next-themes";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { v2Strings } from "./strings";

// System, light, or dark. next-themes keeps the choice in localStorage (care-pi.theme).
export function ThemeToggle({
  className,
  labels = v2Strings.theme,
}: {
  className?: string;
  labels?: typeof v2Strings.theme;
}) {
  const { theme = "system", setTheme } = useTheme();
  const names: Record<string, string> = { system: labels.system, light: labels.light, dark: labels.dark };
  const Icon = theme === "light" ? SunIcon : theme === "dark" ? MoonIcon : LaptopIcon;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className={cn("no-underline", className)}
          aria-label={`${labels.label}: ${names[theme] ?? theme}`}
        >
          <Icon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{labels.label}</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(String(v))}>
            <DropdownMenuRadioItem value="system">
              <LaptopIcon />
              {labels.system}
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="light">
              <SunIcon />
              {labels.light}
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="dark">
              <MoonIcon />
              {labels.dark}
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
