import { useEffect, useReducer, useRef, useState } from "react";
import { api, apiUrl, call, root } from "@/api/client";
import { asProblem, type ProblemError } from "@/api/problem";
import type { Job, JobEventPage } from "@/api/types";
import { applyJobEvent, seedJobView, type JobEvent, type JobEventName, type JobView } from "./job-state";

type Action = { type: "event"; ev: JobEvent } | { type: "cursor"; id: number };

function reducer(view: JobView, action: Action): JobView {
  if (action.type === "cursor") return action.id > view.lastEventId ? { ...view, lastEventId: action.id } : view;
  return applyJobEvent(view, action.ev);
}

const EVENTS: JobEventName[] = ["step", "log", "progress", "error", "done"];

export type StreamState = "idle" | "open" | "retry" | "poll" | "error";

export interface JobStreamOptions {
  /** S67: the public stream of a device without a session (GET /_care-pi/jobs/{id}/events): steps only, no logs. */
  public?: boolean;
}

// A 4xx problem other than 429 does not change when the console asks again
// (for example 404 job-not-found or 401 not-authenticated), so the poll stops.
function permanent(problem: ProblemError | null): boolean {
  return problem !== null && problem.status >= 400 && problem.status < 500 && problem.status !== 429;
}

function eventsPath(jobId: string, isPublic: boolean): string {
  const id = encodeURIComponent(jobId);
  return isPublic ? `/_care-pi/jobs/${id}/events` : apiUrl(`/jobs/${id}/events`);
}

// Follows the events of a job (SSE). A job from GET /jobs/{id} (with log_tail) gives the steps and the tail at once,
// and the stream starts after its last_event_id (docs/api/README.md section 6.4); a finished job needs no stream.
// Without it, the stream starts at ?after=0 and shows each step again (section 6.2). When the stream closes before
// the event done, it reads the JSON form of the same URL each 2 s. A permanent problem stops the poll: the state is
// "error", and problem has the problem.
// The caller must give the component a new key for a new job id (JobProgress does).
export function useJobStream(jobId: string | null, initial: Job | null = null, options: JobStreamOptions = {}) {
  const [view, dispatch] = useReducer(reducer, initial, seedJobView);
  const [state, setState] = useState<StreamState>("idle");
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const last = useRef(view.lastEventId);
  const done = useRef(view.done);
  const isPublic = Boolean(options.public);

  useEffect(() => {
    last.current = view.lastEventId;
    done.current = view.done;
  }, [view.lastEventId, view.done]);

  useEffect(() => {
    if (!jobId || done.current) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const page = async (): Promise<JobEventPage> => {
      const init = {
        params: { path: { job_id: jobId }, query: { after: last.current } },
        headers: { Accept: "application/json" },
      };
      if (isPublic) return (await call(root.GET("/_care-pi/jobs/{job_id}/events", init))) as JobEventPage;
      return (await call(api.GET("/jobs/{job_id}/events", init))) as JobEventPage;
    };

    const poll = async () => {
      if (stopped || done.current) return;
      setState("poll");
      try {
        const p = await page();
        for (const ev of p.events) {
          dispatch({ type: "event", ev: { id: "id" in ev ? ev.id : null, event: ev.event, data: ev.data } });
        }
        // The public page counts the events that it does not show: the next page starts after them.
        dispatch({ type: "cursor", id: p.last_event_id ?? 0 });
        last.current = Math.max(last.current, p.last_event_id ?? 0);
        if (p.done) return;
      } catch (error) {
        // The global handler also deals with an ended session (401 not-authenticated).
        const pr = asProblem(error);
        if (permanent(pr)) {
          if (!stopped) {
            setProblem(pr);
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

    const source = new EventSource(`${eventsPath(jobId, isPublic)}?after=${last.current}`);
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
  }, [jobId, isPublic]);

  return { view, state, problem };
}
