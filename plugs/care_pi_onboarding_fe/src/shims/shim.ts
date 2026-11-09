/**
 * ESM shim for `use-sync-external-store/shim`.
 *
 * The real package is CommonJS-only and does `require("react")`. Under
 * @originjs/vite-plugin-federation a CJS `require("react")` is NOT rewritten to
 * the shared (host) React, so it binds to the remote's *bundled* React whose
 * dispatcher is null once the host renders the tree:
 *   "Cannot read properties of null (reading 'useSyncExternalStore')"
 *
 * `@base-ui/utils/store/useStore` imports this for every Base UI store, which
 * is why it fires on the first dialog/popover that mounts.
 *
 * React 19 has `useSyncExternalStore` built in, so delegating to a bare `react`
 * import is enough. Note this must be a real function that reads React at CALL
 * time — a bare `export { useSyncExternalStore } from "react"` gets re-bound by
 * Rollup to the bundled React copy and reintroduces the bug.
 */
import * as React from "react";

export function useSyncExternalStore<Snapshot>(
  subscribe: (onStoreChange: () => void) => () => void,
  getSnapshot: () => Snapshot,
  getServerSnapshot?: () => Snapshot,
): Snapshot {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export default { useSyncExternalStore };
