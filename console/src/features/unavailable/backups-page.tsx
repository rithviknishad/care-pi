import { strings } from "@/lib/strings";
import { UnavailablePage } from "./unavailable-page";

export default function BackupsPage() {
  return <UnavailablePage feature="backups" title={strings.unavailable.backups} />;
}
