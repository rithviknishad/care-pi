import { EyeIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group";
import { FocusBackdrop, FocusBar, FocusCard } from "../focus";
import { WhatNext } from "../panel";
import { ThemeToggle } from "../theme-toggle";
import { device } from "./sample-data";

export function FocusBrand() {
  return (
    <span className="flex h-9 min-w-0 items-center gap-2 rounded-full bg-scrim-full px-3 pl-1.5">
      <img src="/favicon.svg" alt="" className="size-6" width={24} height={24} />
      <span className="font-semibold">care-pi</span>
      <span className="truncate type-data text-on-gradient-muted">{device.name}</span>
    </span>
  );
}

export function FocusEnd() {
  return (
    <span className="rounded-full bg-scrim-full">
      <ThemeToggle className="text-on-gradient hover:bg-white/10" />
    </span>
  );
}

// Claim (Focus layout): one card, one task, on the gradient band.
export function FocusExample() {
  return (
    <FocusBackdrop top={<FocusBar start={<FocusBrand />} end={<FocusEnd />} />}>
      <FocusCard
        illustration="pi-box"
        title="Claim this device"
        description="You are the first person to open this console. Prove that you own the device, then make a password."
        footer="Lost the claim code? It is on the screen of the device, or in the Imager settings."
      >
        <form className="flex flex-col gap-5" onSubmit={(e) => e.preventDefault()}>
          <Field>
            <FieldLabel htmlFor="claim-code">Claim code</FieldLabel>
            <Input
              id="claim-code"
              className="font-mono tracking-widest uppercase"
              placeholder="XXXX-XXXX-XXXX"
              autoComplete="off"
            />
            <FieldDescription>12 letters and digits. Dashes are not necessary.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="new-password">New console password</FieldLabel>
            <InputGroup>
              <InputGroupInput id="new-password" type="password" autoComplete="new-password" />
              <InputGroupAddon align="inline-end">
                <InputGroupButton size="icon-xs" aria-label="Show the password">
                  <EyeIcon />
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
            <FieldDescription>
              12 characters or more. A phrase of words is strong and easy to remember.
            </FieldDescription>
          </Field>
          <div className="flex flex-col gap-2">
            <Button type="submit" size="lg">
              Claim the device
            </Button>
            <WhatNext>Next, you save 8 recovery codes. Then the setup starts.</WhatNext>
          </div>
        </form>
      </FocusCard>
    </FocusBackdrop>
  );
}
