import { toast } from "sonner";
import { Button } from "@/components/ui/button";

export function ToastExample() {
  return (
    <div className="flex flex-wrap gap-2">
      <Button
        variant="outline"
        onClick={() => toast.success("Settings saved", { description: "Care restarts in the background." })}
      >
        Success
      </Button>
      <Button variant="outline" onClick={() => toast.info("A new version is ready")}>
        Info
      </Button>
      <Button variant="outline" onClick={() => toast.warning("The disk is almost full")}>
        Warning
      </Button>
      <Button variant="outline" onClick={() => toast.error("The copy did not finish")}>
        Error
      </Button>
    </div>
  );
}
