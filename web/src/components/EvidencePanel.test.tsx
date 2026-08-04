import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import EvidencePanel from "./EvidencePanel";
import type { EvidenceResponse } from "../api/types";

const evidence: EvidenceResponse = {
  id: "evidence-uuid-1",
  hash: "sha256:abcdefabcdefabcdefabcdefabcdef",
  windowStart: "2026-08-01T09:30:00Z",
  windowEnd: "2026-08-01T10:00:00Z",
  partial: false,
  items: [
    {
      id: "event-1",
      kind: "KubernetesEvent",
      source: "k8s",
      timestamp: "2026-08-01T09:55:00Z",
      summary: "ContainerOOMKilled 内存超限",
    },
  ],
};

describe("EvidencePanel", () => {
  it("无证据显示空状态", () => {
    render(<EvidencePanel />);
    expect(screen.getByText("尚无证据")).toBeInTheDocument();
  });

  it("详情不可用时显示降级提示", () => {
    render(
      <EvidencePanel
        evidence={{ id: "x", hash: "sha256:y", detailsUnavailable: true }}
      />,
    );
    expect(screen.getByText(/证据详情暂不可用/)).toBeInTheDocument();
  });

  it("展示证据条目与哈希", () => {
    render(<EvidencePanel evidence={evidence} />);
    expect(screen.getByText("KubernetesEvent")).toBeInTheDocument();
    expect(screen.getByText(/ContainerOOMKilled 内存超限/)).toBeInTheDocument();
    expect(screen.getByText(/sha256:abcdef/)).toBeInTheDocument();
  });

  it("部分缺失与脱敏提示", () => {
    render(
      <EvidencePanel
        evidence={{
          ...evidence,
          partial: true,
          missingSources: ["loki"],
          redactions: [{ kind: "password", count: "1" }],
        }}
      />,
    );
    expect(screen.getByText(/缺失来源: loki/)).toBeInTheDocument();
    expect(screen.getByText(/已脱敏 1 处/)).toBeInTheDocument();
  });
});
