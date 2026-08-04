import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import AuditTimeline from "./AuditTimeline";
import type { TimelineEntry } from "../api/types";

const entries: TimelineEntry[] = [
  {
    time: "2026-08-01T10:00:00Z",
    type: "PhaseTransition",
    reason: "Detected→CollectingEvidence",
    actor: "aegisops-operator",
    sequence: 1,
    eventHash: "1a2b3c4d5e6f",
  },
];

describe("AuditTimeline", () => {
  it("空时间线显示空状态", () => {
    render(<AuditTimeline items={[]} />);
    expect(screen.getByText("暂无时间线")).toBeInTheDocument();
  });

  it("详情不可用显示降级提示", () => {
    render(<AuditTimeline detailsUnavailable />);
    expect(screen.getByText(/审计时间线暂不可用/)).toBeInTheDocument();
  });

  it("展示时间线条目与来源标注", () => {
    render(<AuditTimeline items={entries} source="audit" />);
    expect(screen.getByText("PhaseTransition")).toBeInTheDocument();
    expect(screen.getByText("@aegisops-operator")).toBeInTheDocument();
    expect(screen.getByText("#1a2b3c4d5e6f")).toBeInTheDocument();
    expect(screen.getByText(/来源: 诊断审计/)).toBeInTheDocument();
  });
});
