import { useEffect, useReducer, useRef, useState } from "react";
import { api, apiUrl, call } from "@/api/client";
import { asProblem, type ProblemError } from "@/api/problem";
import type { Job, JobEventPage } from "@/api/types";
import { applyJobEvent, emptyJobView, type JobEvent, type JobEventName, type JobView } from "./job-state";

type Action = { type: "event"; ev: JobEvent };

function reducer(view: JobView, action: Action): JobView {
  return applyJobEvent(view, action.ev);
}

const EVENTS: JobEventName[] = ["step", "log", "progress", "error", "done"];

export type StreamState = "idle" | "open" | "retry" | "poll" | "error";

// A 4xx problem other than 429 does not change when the console asks again
// (for example 404 job-not-found or 401 not-authenticated), so the poll stops.
function permanent(problem: ProblemError | null): boolean {
  return problem !== null && problem.status >= 400 && problem.status < 500 && problem.status !== 429;
}

// Follows GET /jobs/{id}/events (SSE). After a page reload it starts at ?after=0, so it
// shows each step again (docs/api/README.md section 6.2). When the stream closes before
// the event done, it reads the JSON form of the same URL each 2 s. A permanent problem
// stops the poll: the state is "error", and problem has the problem.
// The caller must give the component a new key for a new job id (JobProgress does).
export function useJobStream(jobId: string | null, initial: Job | null = null) {
  const [view, dispatch] = useReducer(reducer, initial, emptyJobView);
  const [state, setState] = useState<StreamState>("idle");
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const last = useRef(0);
  const done = useRef(false);

  useEffect(() => {
    last.current = view.lastEventId;
    done.current = view.done;
  }, [view.lastEventId, view.done]);

  useEffect(() => {
    if (!jobId) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const poll = async () => {
      if (stopped || done.current) return;
      setState("poll");
      try {
        const page = (await call(
          api.GET("/jobs/{job_id}/events", {
            params: { path: { job_id: jobId }, query: { after: last.current } },
            headers: { Accept: "application/json" },
          }),
        )) as JobEventPage;
        for (const ev of page.events) {
          dispatch({ type: "event", ev: { id: "id" in ev ? ev.id : null, event: ev.event, data: ev.data } });
        }
        if (page.done) return;
      } catch (error) {
        // The global handler also deals with an ended session (401 not-authenticated).
        const p = asProblem(error);
        if (permanent(p)) {
          if (!stopped) {
            setProblem(p);
            setState("error");
          }
          return;
        }
        // No connection, 429, or 5xx: try again later.
      }
      timer = setTimeout(poll, 2000);
    };

    if (typeof EventSource === "undefined") {
      void poll();
      return () => {
        stopped = true;
        clearTimeout(timer);
      };
    }

    const source = new EventSource(apiUrl(`/jobs/${encodeURIComponent(jobId)}/events`, { after: 0 }));
    for (const name of EVENTS) {
      source.addEventListener(name, (e) => {
        const msg = e as MessageEvent<string>;
        let data: unknown;
        try {
          data = JSON.parse(msg.data);
        } catch {
          return;
        }
        dispatch({ type: "event", ev: { id: msg.lastEventId ? Number(msg.lastEventId) : null, event: name, data } });
        if (name === "done") {
          done.current = true;
          source.close();
          setState("idle");
        }
      });
    }
    source.onopen = () => setState("open");
    source.onerror = () => {
      if (done.current) return;
      if (source.readyState === EventSource.CLOSED) {
        void poll();
      } else {
        setState("retry");
      }
    };
    return () => {
      stopped = true;
      clearTimeout(timer);
      source.close();
    };
  }, [jobId]);

  return { view, state, problem };
}
