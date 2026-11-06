// The 8 illustrations of v2 (console/design.md, "Illustrations"). The SVG files in ./illustrations are the source:
// the build inlines their text, so an illustration has the colours of the theme (--illo-* tokens) and needs no
// request. public/illustrations has a copy of each file for plain pages (for example the maintenance page of
// Caddy); __tests__/illustrations.test.ts fails when a copy differs. After a change, run:
//   cp src/components/v2/illustrations/*.svg public/illustrations/
import backupVault from "./illustrations/backup-vault.svg?raw";
import clinic from "./illustrations/clinic.svg?raw";
import clock from "./illustrations/clock.svg?raw";
import network from "./illustrations/network.svg?raw";
import phoneQr from "./illustrations/phone-qr.svg?raw";
import piBox from "./illustrations/pi-box.svg?raw";
import shieldCertificate from "./illustrations/shield-certificate.svg?raw";
import updateArrow from "./illustrations/update-arrow.svg?raw";

export const illustrations = {
  "pi-box": piBox,
  "shield-certificate": shieldCertificate,
  "backup-vault": backupVault,
  "update-arrow": updateArrow,
  clinic,
  "phone-qr": phoneQr,
  clock,
  network,
} as const;

export type IllustrationName = keyof typeof illustrations;

export const illustrationNames = Object.keys(illustrations) as IllustrationName[];
