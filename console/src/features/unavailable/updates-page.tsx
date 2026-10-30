import { strings } from "@/lib/strings";
import { UnavailablePage } from "./unavailable-page";

export default function UpdatesPage() {
  return <UnavailablePage feature="updates" title={strings.unavailable.updates} />;
}
