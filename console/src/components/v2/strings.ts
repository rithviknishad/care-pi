// The texts of the v2 components, in ASD-STE100 Simplified Technical English.
// A page gives its own texts as props; these are the defaults. src/lib/strings.ts can import this object
// (for example as strings.v2) when the pages move to v2, so all text stays in one tree.
// Placeholders have the form {name}: fill them with fmt() of src/lib/strings.ts.

export const v2Strings = {
  copy: "Copy",
  copied: "Copied",
  copiedToast: "Copied. You can paste it into a message to support.",
  copyFailed: "The browser did not allow the copy. Select the text, then copy it.",
  copyHeader: {
    device: "Device",
    version: "Agent version",
    time: "Time",
    page: "Page",
  },
  technicalDetails: "Technical details",
  retry: "Try again",
  loading: "Loading",
  recommended: "Recommended",
  shell: {
    main: "Main",
    more: "More",
    menu: "Menu",
    openMenu: "Open the menu",
    skipToContent: "Go to the content",
    device: "Device",
  },
  theme: {
    label: "Theme",
    system: "System",
    light: "Light",
    dark: "Dark",
  },
  stage: {
    stepOf: "Step {current} of {total}",
    steps: "Setup steps",
    done: "Done",
    current: "Now",
    todo: "Not started",
    progress: "{percent} % done",
  },
  steps: {
    pending: "Waiting",
    running: "Running",
    done: "Done",
    failed: "Did not finish",
    skipped: "Skipped",
    usually: "Usually about {time}",
    showLog: "Show the log",
    hideLog: "Hide the log",
  },
  metric: {
    usedOf: "{used} of {cap}",
    noCap: "{used}, no limit",
  },
  explainer: {
    title: "How we keep your copies",
    now: "Now",
    ago: "{span} ago",
    every: {
      minute: "Every minute",
      minutes: "Every {n} minutes",
      hour: "Every hour",
      hours: "Every {n} hours",
      day: "Every day",
    },
    save: "{every} we save a copy.",
    canGoBack: "You can go back {clauses}.",
    onlyNewest: "You can go back to the newest copy only.",
    clause: {
      moment: "to any moment in the last {span}",
      day: "to any day in the last {span}",
      week: "to any week in the last {span}",
      month: "to any month in the last {span}",
    },
    zone: {
      moment: "Each copy",
      day: "1 each day",
      week: "1 each week",
      month: "1 each month",
    },
    and: "and",
    span: {
      hours24: "24 hours",
      day: "day",
      days: "{n} days",
      week: "week",
      weeks: "{n} weeks",
      month: "month",
      months: "{n} months",
      year: "year",
      years: "{n} years",
    },
  },
};

export type V2Strings = typeof v2Strings;
