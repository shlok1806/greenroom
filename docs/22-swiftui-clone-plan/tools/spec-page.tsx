"use client";

/* Scratch-only ground-truth page: renders exactly one Beautiful UI piece,
 * isolated, on a flat --page surface, for the Greenroom clone-spec extractor. */

import { useEffect, useState } from "react";
import { Button } from "@/components/atoms/Button";
import { Chip } from "@/components/atoms/Chip";
import { ProgressRing } from "@/components/atoms/ProgressRing";
import { SegmentedControl } from "@/components/atoms/SegmentedControl";
import { Shimmer } from "@/components/atoms/Shimmer";
import { StatusPill } from "@/components/atoms/StatusPill";
import { StreamText } from "@/components/atoms/StreamText";
import ChatComposer from "@/components/primitives/ChatComposer";
import LoadingState from "@/components/primitives/LoadingState";
import TaskRows from "@/components/primitives/TaskRows";
import ThinkingState from "@/components/primitives/ThinkingState";
import ToolChips from "@/components/primitives/ToolChips";

function Piece({ c, v }: { c: string; v: string }) {
  switch (c) {
    case "task-rows":
      return <TaskRows variant={v || "Capsules"} />;
    case "thinking-state":
      return <ThinkingState variant={v || "Steps"} />;
    case "tool-chips":
      return <ToolChips />;
    case "loading-state":
      return <LoadingState variant={v || "Drive"} />;
    case "chat-composer":
      return <ChatComposer />;
    case "shimmer":
      return <Shimmer className="text-[13px] font-medium">Checking</Shimmer>;
    case "status-pill":
      return (
        <div className="flex gap-2">
          {(["neutral", "accent", "green", "orange", "red"] as const).map((t) => (
            <StatusPill key={t} tone={t}>
              {t}
            </StatusPill>
          ))}
        </div>
      );
    case "button":
      return (
        <div className="flex flex-col gap-3">
          {(["xs", "sm", "md"] as const).map((s) => (
            <div key={s} className="flex gap-2">
              {(["primary", "secondary", "ghost", "accent", "success", "quiet"] as const).map(
                (vv) => (
                  <Button key={vv} variant={vv} size={s} data-variant={vv} data-size={s}>
                    {vv}
                  </Button>
                ),
              )}
            </div>
          ))}
        </div>
      );
    case "progress-ring":
      return (
        <div className="flex gap-3">
          <ProgressRing progress={0.66} tone="accent">
            2
          </ProgressRing>
          <ProgressRing progress={1} tone="green">
            4
          </ProgressRing>
          <ProgressRing progress={0.25} tone="red">
            1
          </ProgressRing>
        </div>
      );
    case "stream-text":
      return (
        <p className="max-w-80 text-[13px] leading-normal text-ink">
          <StreamText text="The verifier opened TipSplit and set the tip to 25 percent." />
        </p>
      );
    case "segmented-control":
      return (
        <SegmentedControl
          options={["Checks", "Activity", "Logs"] as const}
          value="Activity"
          onChange={() => {}}
        />
      );
    case "chip":
      return <Chip>updated_at</Chip>;
    default:
      return <p>unknown piece {c}</p>;
  }
}

export default function SpecPage() {
  const [q, setQ] = useState<{ c: string; v: string } | null>(null);
  useEffect(() => {
    const p = new URLSearchParams(window.location.search);
    setQ({ c: p.get("c") ?? "", v: p.get("v") ?? "" });
  }, []);
  if (!q) return null;
  return (
    <div style={{ background: "var(--page)", minHeight: "100vh", padding: 24 }}>
      <div id="spec-root" style={{ display: "inline-block" }}>
        <Piece c={q.c} v={q.v} />
      </div>
    </div>
  );
}
