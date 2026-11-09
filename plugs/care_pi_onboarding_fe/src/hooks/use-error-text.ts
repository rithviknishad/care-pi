import { useTranslation } from "@/hooks/use-translation";
import { ApiError } from "@/lib/api";

/** One sentence for a failed request. The plug never refreshes the host token (a 401 asks for a new sign-in). */
export function useErrorText() {
  const { t } = useTranslation();
  return (error: unknown): string => {
    if (error instanceof ApiError) {
      if (error.status === 401) return t("error_session");
      if (error.status === 403) return error.message || t("error_denied");
      if (error.status === 429) return t("error_rate");
      if (error.status === 0) return t("error_network");
      return error.message || t("error_unknown", { status: error.status });
    }
    return t("error_network");
  };
}
