import { Copy } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useTranslation } from "@/hooks/use-translation";
import { type StepResult, totals } from "@/lib/wizard";

function kindLabel(kind: string): string {
  return kind.replace(/_/g, " ");
}

export function ResultPanel({ result }: { result: StepResult }) {
  const { t } = useTranslation();
  const credentials = result.secrets?.credentials ?? [];

  if (result.status === "failed") {
    return (
      <Alert variant="destructive" data-testid="result-failed">
        <AlertTitle>{t("result_failed_title")}</AlertTitle>
        <AlertDescription>
          <p>{result.detail}</p>
          <p className="mt-1">{t("result_failed_hint")}</p>
        </AlertDescription>
      </Alert>
    );
  }

  const kinds = Object.keys({ ...result.created, ...result.existing });
  const copyAll = async () => {
    const text = credentials
      .map((c) => `${c.username}\t${c.password}`)
      .join("\n");
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t("credentials_copied"));
    } catch {
      toast.error(t("credentials_copy_failed"));
    }
  };

  return (
    <div className="flex flex-col gap-4" data-testid="result-done">
      <Alert>
        <AlertTitle>
          {result.status === "skipped"
            ? t("result_skipped_title")
            : result.status === "removed"
              ? t("result_removed_title")
              : totals(result.created) > 0
                ? t("result_done_title", { count: totals(result.created) })
                : t("result_nothing_new_title")}
        </AlertTitle>
        <AlertDescription>{result.detail}</AlertDescription>
      </Alert>
      {kinds.length > 0 && (
        <div className="flex flex-wrap gap-2" data-testid="result-counts">
          {kinds.map((kind) => (
            <Badge key={kind} variant="neutral">
              {t("count_line", {
                kind: kindLabel(kind),
                created: result.created[kind] ?? 0,
                existing: result.existing[kind] ?? 0,
              })}
            </Badge>
          ))}
        </div>
      )}
      {credentials.length > 0 && (
        <div className="flex flex-col gap-3" data-testid="credentials">
          <Alert variant="warning">
            <AlertTitle>{t("credentials_title")}</AlertTitle>
            <AlertDescription>{t("credentials_warning")}</AlertDescription>
          </Alert>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("field_username")}</TableHead>
                <TableHead>{t("field_password")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {credentials.map((c) => (
                <TableRow key={c.username}>
                  <TableCell>{c.username}</TableCell>
                  <TableCell className="font-mono">{c.password}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <div>
            <Button type="button" variant="outline" size="sm" onClick={copyAll}>
              <Copy aria-hidden="true" />
              {t("credentials_copy")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
