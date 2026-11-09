import { createContext, useContext } from "react";

export const ContainerRefContext =
  createContext<React.RefObject<HTMLDivElement | null> | null>(null);

export const useContainerRef = () => {
  const context = useContext(ContainerRefContext);
  if (!context)
    throw new Error(
      "useContainerRef must be used within a ContainerRefProvider",
    );
  return context;
};

/**
 * Returns the `container` prop for a Base UI `Portal`.
 *
 * Portals default to mounting on `document.body`, which sits outside
 * `.care-pi-onboarding-fe-container`. Since every design token is declared *on*
 * that container (the PostCSS pass in vite.config.ts rewrites Tailwind's `:root`
 * to it), portalled content mounted on `document.body` inherits none of them and
 * renders with `bg-popover`, `bg-card`, `text-foreground` etc. all resolving to
 * nothing. Anchoring the portal inside the container keeps tokens inherited.
 *
 * Unlike `useContainerRef` this does not throw when no provider is present, so
 * the UI primitives stay usable outside the plugin shell (e.g. the standalone
 * dev harness in `main.tsx`). Without a provider it returns `undefined`, which
 * is Base UI's "use the default container" value.
 */
export const usePortalContainer = () => {
  const context = useContext(ContainerRefContext);
  return context ?? undefined;
};
