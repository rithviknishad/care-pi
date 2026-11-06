import { PlusIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "../empty-state";

export function EmptyStateExample() {
  return (
    <EmptyState
      illustration="backup-vault"
      title="No backups yet"
      description="Add a place for the copies: a USB disk, a network folder, or the cloud."
      action={
        <Button>
          <PlusIcon data-icon="inline-start" />
          Add a place
        </Button>
      }
    />
  );
}
