import { ArrowRightIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Hero } from "../hero";
import { useArrived } from "./arrival";

export function HeroExample() {
  const arrived = useArrived();
  return (
    <div className="flex flex-col gap-4">
      <Hero
        as="h2"
        eyebrow="care.local"
        title="Care is ready"
        description="Staff can now open Care at https://care.local from each computer and phone of the clinic."
        illustration="clinic"
        actions={
          <Button variant="secondary">
            Open Care
            <ArrowRightIcon data-icon="inline-end" />
          </Button>
        }
      />
      <Hero
        as="h2"
        size="compact"
        title="All good"
        description="Care runs. The last copy of the data is 12 min old."
        illustration="pi-box"
        loading={!arrived}
        testId="hero-compact"
      />
    </div>
  );
}
