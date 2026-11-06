import { BuildingIcon, UserRoundIcon, UsersRoundIcon } from "lucide-react";
import { useState } from "react";
import { ChoiceCards } from "../choice-cards";

export function ChoiceCardsExample() {
  const [value, setValue] = useState<string | null>("small-clinic");
  return (
    <ChoiceCards
      name="preset"
      legend="How many people use Care at the same time?"
      description="You can change this later in Settings."
      value={value}
      onChange={setValue}
      choices={[
        {
          value: "solo",
          title: "Just me",
          fit: "1 or 2 people at a time.",
          icon: UserRoundIcon,
          meta: "2 GB device is enough",
        },
        {
          value: "small-clinic",
          title: "Small clinic",
          fit: "3 or 4 staff at the same time.",
          icon: UsersRoundIcon,
          recommended: true,
          meta: "4 GB device is better",
        },
        {
          value: "busy-counter",
          title: "Busy counter",
          fit: "5 to 10 staff at the same time.",
          icon: BuildingIcon,
          meta: "4 GB device or more",
        },
      ]}
    />
  );
}
