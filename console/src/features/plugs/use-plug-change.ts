import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, call } from "@/api/client";
import { qk } from "@/api/queries";
import type { Job, Plug, PlugChange, PlugChangeResult } from "@/api/types";

// The flow of a plug change (docs/api/README.md section 14.7): PUT /plugs/{id}?dry_run=true gives the plan and the
// ETag of config.yaml; the dialog shows the plan; PUT /plugs/{id}?apply=true with the same body and If-Match on that
// ETag writes the change and starts a plug-change job.
export function usePlugChange(plug: Pick<Plug, "id">) {
  const qc = useQueryClient();
  const [request, setRequest] = useState<PlugChange | null>(null);
  const [preview, setPreview] = useState<PlugChangeResult | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const dry = useMutation({
    mutationFn: (body: PlugChange) =>
      call(api.PUT("/plugs/{plug_id}", { params: { path: { plug_id: plug.id }, query: { dry_run: true } }, body })),
    onMutate: (body) => {
      setRequest(body);
      setPreview(null);
    },
    onSuccess: setPreview,
    onError: () => setRequest(null),
  });
  const apply = useMutation({
    mutationFn: () => {
      if (!request || !preview) throw new Error("no preview");
      return call(
        api.PUT("/plugs/{plug_id}", {
          params: { path: { plug_id: plug.id }, query: { apply: true }, header: { "If-Match": preview.etag } },
          body: request,
        }),
      );
    },
    onSuccess: (res) => {
      setPreview(null);
      setRequest(null);
      if (res.job) setJob(res.job);
      void qc.invalidateQueries({ queryKey: qk.plugs });
      void qc.invalidateQueries({ queryKey: qk.publicStatus });
    },
  });
  const cancel = () => {
    setPreview(null);
    setRequest(null);
    apply.reset();
  };
  return { request, preview, job, setJob, dry, apply, cancel };
}

export type PlugChangeFlow = ReturnType<typeof usePlugChange>;
