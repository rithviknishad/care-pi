import { CircleCheckIcon, InfoIcon, Loader2Icon, OctagonXIcon, TriangleAlertIcon } from "lucide-react";
import { useTheme } from "next-themes";
import { Toaster as Sonner, type ToasterProps } from "sonner";

// The v2 toaster (sonner). src/styles/motion.css gives each toast the v2 card look, also the careui Toaster.
// Use toast() of sonner: toast.success(text), toast.error(text). A toast confirms; it never holds the only
// copy of a problem (a problem is a ProblemCard in the page).
export function Toaster(props: ToasterProps) {
  const { theme = "system" } = useTheme();
  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      position="top-center"
      offset={{ top: "calc(var(--topbar-height) + 0.5rem)" }}
      mobileOffset={{ top: "calc(var(--topbar-height) + 0.5rem)" }}
      icons={{
        success: <CircleCheckIcon className="size-5" />,
        info: <InfoIcon className="size-5" />,
        warning: <TriangleAlertIcon className="size-5" />,
        error: <OctagonXIcon className="size-5" />,
        loading: <Loader2Icon className="size-5 motion-ok:animate-spin" />,
      }}
      {...props}
    />
  );
}
