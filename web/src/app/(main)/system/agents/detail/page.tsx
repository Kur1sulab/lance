"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { AgentEditor } from "@/components/agent-editor";
import { StateBoundary } from "@/components/data/state-boundary";
import { PageShell } from "@/components/shell/page-shell";
import { Button } from "@/components/ui/button";
import { usePermissionDenied } from "@/hooks/use-permission-denied";

// Deep-link editor page for one agent. The primary flow is the drawer on
// /system/agents; this page reuses the same AgentEditor component full-width so a
// direct URL (or an external link) still opens the editor.
function AgentDetailInner() {
  const searchParams = useSearchParams();
  const tn = useTranslations("nav");
  const tp = useTranslations("pageStates");
  const perm = usePermissionDenied();
  const key = searchParams.get("key") ?? "";

  return (
    <PageShell
      title={tn("item.agents")}
      description={key || undefined}
      breadcrumb={[{ label: tn("group.capability"), href: "/dashboard" }, { label: tn("item.agents") }]}
      actions={
        <Button asChild variant="ghost" size="icon-sm">
          <Link href="/system/agents" aria-label={tn("item.agents")}>
            <ArrowLeftIcon className="size-4" />
          </Link>
        </Button>
      }
    >
      <div className="flex h-full min-h-0 flex-col px-4 py-3 md:px-6">
        <StateBoundary
          // 本页没有自己的取数：AgentEditor 自带骨架与错误提示，页面只负责
          // 「没带 key」这一种空态，以及外层的权限壳。
          permission={perm.denied ? "agentDetail:read" : null}
          permissionProps={{
            title: tp("permissionTitle"),
            detail: tp("permissionDetail", { name: tn("item.agents"), ask: tp("askAdmin") }),
            missingLabel: tp("permissionMissing"),
            askLabel: tp("permissionAsk"),
            ask: tp("askAdmin"),
          }}
          empty={!key}
          emptyProps={{ title: tp("emptyTitle"), reason: tp("empty.agentDetail") }}
        >
          <div className="flex min-h-0 flex-1 flex-col rounded-lg border">
            <AgentEditor agentKey={key} />
          </div>
        </StateBoundary>
      </div>
    </PageShell>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function AgentDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <AgentDetailInner />
    </React.Suspense>
  );
}
