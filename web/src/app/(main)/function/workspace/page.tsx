"use client";

import * as React from "react";

import {
  DownloadIcon,
  FileIcon,
  FolderIcon,
  FolderPlusIcon,
  HardDriveIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { StateBoundary } from "@/components/data/state-boundary";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { usePermissionDenied } from "@/hooks/use-permission-denied";
import { getDateLocale } from "@/i18n/datetime";
import { api } from "@/lib/api";
import { errorCodeOf } from "@/lib/api-error";
import type { WorkspaceEntry, WorkspaceFile } from "@/lib/types";
import { cn } from "@/lib/utils";

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}
function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString(getDateLocale(), {
    year: "2-digit",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

type EditState = {
  file: WorkspaceFile;
  content: string;
  dirty: boolean;
  saving: boolean;
};

export default function WorkspacePage() {
  const t = useTranslations("workspace");
  const tn = useTranslations("nav");
  const tps = useTranslations("pageStates");
  const perm = usePermissionDenied();
  const lastError = React.useRef<unknown>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [path, setPath] = React.useState("");
  const [entries, setEntries] = React.useState<WorkspaceEntry[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [edit, setEdit] = React.useState<EditState | null>(null);
  const [mkdirOpen, setMkdirOpen] = React.useState(false);
  const [mkdirName, setMkdirName] = React.useState("");
  const uploadRef = React.useRef<HTMLInputElement>(null);

  const load = React.useCallback(
    (p: string) => {
      setLoading(true);
      api
        .workspaceList(p)
        .then((r) => {
          setEntries(r.entries);
          setPath(r.path);
          setError(null);
          lastError.current = null;
          perm.reset();
        })
        .catch((e) => {
          lastError.current = e;
          perm.check(e);
          setError((e as Error).message);
          toast.error(t("readDirFailed", { msg: (e as Error).message }));
        })
        .finally(() => setLoading(false));
    },
    [t],
  );

  React.useEffect(() => {
    load("");
  }, [load]);

  const crumbs = React.useMemo(() => {
    const parts = path ? path.split("/") : [];
    const acc: { name: string; path: string }[] = [{ name: t("crumbRoot"), path: "" }];
    let cur = "";
    for (const part of parts) {
      cur = cur ? `${cur}/${part}` : part;
      acc.push({ name: part, path: cur });
    }
    return acc;
  }, [path, t]);

  const openFile = (e: WorkspaceEntry) => {
    api
      .workspaceRead(e.path)
      .then((f) => setEdit({ file: f, content: f.content ?? "", dirty: false, saving: false }))
      .catch((err) => toast.error(t("openFileFailed", { msg: (err as Error).message })));
  };

  const saveFile = () => {
    if (!edit) return;
    setEdit({ ...edit, saving: true });
    api
      .workspaceWrite(edit.file.path, edit.content)
      .then(() => {
        toast.success(t("saved"));
        setEdit((cur) => (cur ? { ...cur, dirty: false, saving: false } : cur));
        load(path);
      })
      .catch((err) => {
        toast.error(t("saveFailed", { msg: (err as Error).message }));
        setEdit((cur) => (cur ? { ...cur, saving: false } : cur));
      });
  };

  const del = (e: WorkspaceEntry) => {
    if (!window.confirm(t("confirmDelete", { kind: e.dir ? "dir" : "file", name: e.name }))) return;
    api
      .workspaceDelete(e.path)
      .then(() => {
        toast.success(t("deleted"));
        load(path);
      })
      .catch((err) => toast.error(t("deleteFailed", { msg: (err as Error).message })));
  };

  const doUpload = (files: FileList | null) => {
    if (!files || files.length === 0) return;
    api
      .workspaceUpload(path, Array.from(files))
      .then((r) => {
        toast.success(t("uploaded", { n: r.uploaded }));
        load(path);
      })
      .catch((err) => toast.error(t("uploadFailed", { msg: (err as Error).message })))
      .finally(() => {
        if (uploadRef.current) uploadRef.current.value = "";
      });
  };

  const doMkdir = () => {
    const name = mkdirName.trim();
    if (!name) return;
    const target = path ? `${path}/${name}` : name;
    api
      .workspaceMkdir(target)
      .then(() => {
        toast.success(t("folderCreated"));
        setMkdirOpen(false);
        setMkdirName("");
        load(path);
      })
      .catch((err) => toast.error(t("createFailed", { msg: (err as Error).message })));
  };

  return (
    <PageShell
      title={tn("item.workspace")}
      breadcrumb={[{ label: tn("group.target"), href: "/dashboard" }, { label: tn("item.workspace") }]}
      toolbar={
        <Toolbar
          filters={
            <div className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
          <HardDriveIcon className="text-muted-foreground mr-1 size-4 shrink-0" />
          {crumbs.map((c, i) => (
            <React.Fragment key={c.path}>
              {i > 0 && <span className="text-muted-foreground">/</span>}
              <button
                type="button"
                onClick={() => load(c.path)}
                className={cn(
                  "max-w-[160px] truncate rounded px-1.5 py-0.5 hover:bg-accent",
                  i === crumbs.length - 1 ? "text-foreground font-medium" : "text-muted-foreground",
                )}
              >
                {c.name}
              </button>
            </React.Fragment>
          ))}
            </div>
          }
          viewSwitch={
            <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setMkdirOpen(true)}>
            <FolderPlusIcon /> {t("newFolder")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => uploadRef.current?.click()}>
            <UploadIcon /> {t("upload")}
          </Button>
          <Button variant="ghost" size="icon" className="size-8" onClick={() => load(path)} title={t("refresh")}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <input ref={uploadRef} type="file" multiple className="hidden" onChange={(e) => doUpload(e.target.files)} />
            </div>
          }
        />
      }
    >
      <div className="px-4 py-3 md:px-6">
        <StateBoundary
          className="flex min-h-0 flex-col"
          loading={loading && entries.length === 0}
          error={
            error
              ? {
                  title: tps("errorTitle"),
                  detail: tps("errorDetail"),
                  code: errorCodeOf(lastError.current),
                  codeLabel: tps("errorCodeLabel"),
                  retryLabel: tps("retry"),
                }
              : null
          }
          permission={perm.denied ? "workspace:read" : null}
          permissionProps={{
            title: tps("permissionTitle"),
            detail: tps("permissionDetail", { name: tn("item.workspace"), ask: tps("askAdmin") }),
            missingLabel: tps("permissionMissing"),
            askLabel: tps("permissionAsk"),
            ask: tps("askAdmin"),
          }}
          empty={!loading && !error && entries.length === 0}
          emptyProps={{ title: tps("emptyTitle"), reason: tps("empty.workspace") }}
          onRetry={() => {
            setError(null);
            lastError.current = null;
            perm.reset();
            load(path);
          }}
        >
      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("colName")}</TableHead>
                <TableHead className="w-28 text-right">{t("colSize")}</TableHead>
                <TableHead className="w-40">{t("colMtime")}</TableHead>
                <TableHead className="w-24 text-right">{t("colActions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.path}>
                  <TableCell>
                    <button
                      type="button"
                      onClick={() => (e.dir ? load(e.path) : openFile(e))}
                      className="flex items-center gap-2 text-left hover:underline"
                    >
                      {e.dir ? (
                        <FolderIcon className="text-primary size-4 shrink-0" />
                      ) : (
                        <FileIcon className="text-muted-foreground size-4 shrink-0" />
                      )}
                      <span className="truncate font-mono text-sm">{e.name}</span>
                    </button>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-right font-mono text-xs">
                    {e.dir ? "—" : fmtSize(e.size)}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{fmtTime(e.mtime)}</TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      {!e.dir && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title={t("download")}
                          onClick={() =>
                            api
                              .workspaceDownload(e.path)
                              .catch((err) => toast.error(t("downloadFailed", { msg: (err as Error).message })))
                          }
                        >
                          <DownloadIcon className="size-3.5" />
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive size-7"
                        title={t("delete")}
                        onClick={() => del(e)}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
        </StateBoundary>

      {/* 文件查看 / 编辑 */}
      <Sheet open={edit !== null} onOpenChange={(o) => !o && setEdit(null)}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-2xl">
          {edit && (
            <>
              <SheetHeader className="border-b p-4">
                <SheetTitle className="flex items-center gap-2 truncate font-mono text-sm">
                  <FileIcon className="size-4 shrink-0" />
                  <span className="truncate" title={edit.file.path}>
                    {edit.file.path}
                  </span>
                </SheetTitle>
                <span className="text-muted-foreground text-xs">{fmtSize(edit.file.size)}</span>
              </SheetHeader>

              {edit.file.binary || edit.file.too_large ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
                  <p className="text-muted-foreground text-sm">{edit.file.too_large ? t("tooLarge") : t("binary")}</p>
                  <Button variant="outline" onClick={() => api.workspaceDownload(edit.file.path)}>
                    <DownloadIcon /> {t("downloadFile")}
                  </Button>
                </div>
              ) : (
                <>
                  <div className="min-h-0 flex-1 p-3">
                    <Textarea
                      value={edit.content}
                      onChange={(ev) => setEdit({ ...edit, content: ev.target.value, dirty: true })}
                      spellCheck={false}
                      className="h-full min-h-[50vh] resize-none font-mono text-xs leading-relaxed"
                    />
                  </div>
                  <SheetFooter className="flex-row items-center justify-between border-t p-3">
                    <span className="text-muted-foreground text-xs">{edit.dirty ? t("dirty") : t("synced")}</span>
                    <div className="flex gap-2">
                      <Button variant="outline" onClick={() => api.workspaceDownload(edit.file.path)}>
                        <DownloadIcon /> {t("download")}
                      </Button>
                      <Button onClick={saveFile} disabled={!edit.dirty || edit.saving}>
                        <SaveIcon /> {edit.saving ? t("saving") : t("save")}
                      </Button>
                    </div>
                  </SheetFooter>
                </>
              )}
            </>
          )}
        </SheetContent>
      </Sheet>

      {/* 新建文件夹 */}
      <Dialog open={mkdirOpen} onOpenChange={setMkdirOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{t("newFolder")}</DialogTitle>
          </DialogHeader>
          <Input
            autoFocus
            value={mkdirName}
            onChange={(e) => setMkdirName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && doMkdir()}
            placeholder={t("folderNamePlaceholder")}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setMkdirOpen(false)}>
              {t("cancel")}
            </Button>
            <Button onClick={doMkdir} disabled={!mkdirName.trim()}>
              {t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      </div>
    </PageShell>
  );
}
