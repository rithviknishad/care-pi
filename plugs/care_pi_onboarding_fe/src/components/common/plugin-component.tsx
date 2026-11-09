import { ContainerRefContext } from "@/hooks/use-container-ref";
import { useRef, useState } from "react";

/**
 * Root wrapper for every component this plugin renders into care_fe.
 *
 * The container class carries all design tokens: the PostCSS scoping pass in
 * vite.config.ts rewrites Tailwind's `:root` to `.care-pi-onboarding-fe-container`,
 * so the palette and every careui semantic token resolve *on this element* and
 * inherit down through the subtree.
 *
 * That makes portals a problem. `dialog`, `dropdown-menu` and `tooltip` render
 * through Base UI portals, which mount to `document.body` by default — outside
 * this subtree, where none of the tokens are inherited and every `bg-card` /
 * `bg-popover` resolves to nothing. The ref published here is passed as each
 * portal's `container`, keeping portalled content inside the token scope.
 *
 * `useState` rather than plain `useRef` on purpose: a ref assignment does not
 * re-render, so consumers reading `ref.current` during the first render would
 * see `null` and fall back to `document.body`. Storing the node in state
 * re-renders once it is attached, so portals mount into the container.
 */
export default function PluginComponent({
  children,
}: {
  children: React.ReactNode;
}) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const ref = useRef<HTMLDivElement | null>(null);

  ref.current = container;

  return (
    <div
      className="care-pi-onboarding-fe-container bg-transparent"
      ref={setContainer}
    >
      <ContainerRefContext.Provider value={ref}>
        {children}
      </ContainerRefContext.Provider>
    </div>
  );
}
