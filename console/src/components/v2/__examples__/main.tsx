// The entry of the /__v2 gallery. vite.config.ts serves it on the dev server only (apply: "serve");
// the production build does not include it.
import "@/index.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Gallery } from "./gallery";

const root = document.getElementById("root");
if (import.meta.env.DEV && root) {
  createRoot(root).render(
    <StrictMode>
      <Gallery />
    </StrictMode>,
  );
}
