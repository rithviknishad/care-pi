# Solution locks of CARE Clinic

Each file `<version>.lock.yaml` is one solution lock (`docs/solutions.md`, section 3).
The workflow `solution-lock` makes a lock and publishes it as the release `lock-care-clinic-<version>`.
The owner commits the file here. The agent embeds each file of this directory: it is the floor
of the `stable` and `pinned` tracks without internet.

`withdrawn.yaml` (optional) maps a version to the reason of its withdrawal (section 5.4).
