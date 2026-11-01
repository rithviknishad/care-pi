import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, call } from "@/api/client";
import type { RecoveryKey } from "@/api/types";
import { useReauth } from "@/components/app/reauth-context";

// Invalidates each query of the backups (GET /backups, /backups/health, /restore/points, the disks, the key state),
// the config, the setup, and the status of the dashboard.
export function useInvalidateBackups() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["backups"] });
    void qc.invalidateQueries({ queryKey: ["status"] });
    void qc.invalidateQueries({ queryKey: ["config"] });
    void qc.invalidateQueries({ queryKey: ["setup"] });
  };
}

// Reads the key again (GET /backups/recovery-key, recent authentication). The words stay in memory only.
export function useShowKey(which: "active" | "pending", onKey: (key: RecoveryKey) => void) {
  const { withReauth } = useReauth();
  return useMutation({
    mutationFn: () => withReauth(() => call(api.GET("/backups/recovery-key", { params: { query: { key: which } } }))),
    onSuccess: onKey,
  });
}
