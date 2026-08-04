import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetch, APIError } from "../api/client";
import type { AIOpsIncident } from "../api/types";

interface ApprovalActionsProps {
  incident: AIOpsIncident;
}

function ApprovalActions({ incident }: ApprovalActionsProps) {
  const [reason, setReason] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const queryClient = useQueryClient();

  const phase = incident.status.phase;
  if (phase !== "AwaitingApproval") {
    return null;
  }

  const target = incident.spec.targetRef;
  const proposal = incident.status.proposal;

  const submit = async (decision: "Approve" | "Reject") => {
    if (reason.trim().length < 4) {
      setError("审批理由至少 4 个字符");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await apiFetch(
        `/incidents/${incident.metadata.namespace}/${incident.metadata.name}/approval`,
        {
          method: "POST",
          body: JSON.stringify({ decision, reason: reason.trim() }),
        },
      );
      setDone(decision === "Approve" ? "已批准" : "已拒绝");
      setReason("");
      await queryClient.invalidateQueries({
        queryKey: [
          "incident",
          incident.metadata.namespace,
          incident.metadata.name,
        ],
      });
    } catch (e) {
      setError(e instanceof APIError ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  };

  const openConfirm = () => {
    if (reason.trim().length < 4) {
      setError("审批理由至少 4 个字符");
      return;
    }
    setError(null);
    setConfirming(true);
  };

  return (
    <section className="approval-actions card" aria-label="审批操作">
      <h2>人工审批</h2>
      {done && <p className="approval-done">{done}，等待 Operator 执行。</p>}
      <textarea
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="审批理由（至少 4 个字符）"
        aria-label="审批理由"
        rows={2}
        disabled={submitting || Boolean(done)}
      />
      {error && (
        <p role="alert" className="error-state">
          {error}
        </p>
      )}
      <div className="approval-buttons">
        <button
          type="button"
          className="btn-approve"
          onClick={openConfirm}
          disabled={submitting || Boolean(done)}
        >
          批准并执行
        </button>
        <button
          type="button"
          className="btn-reject"
          onClick={() => submit("Reject")}
          disabled={submitting || Boolean(done)}
        >
          拒绝
        </button>
      </div>
      <p className="approval-hint">
        方案摘要: {incident.status.proposal?.planDigest?.slice(0, 24)}…
      </p>

      {confirming && (
        <div
          className="approval-confirm-overlay"
          role="dialog"
          aria-modal="true"
          aria-label="审批确认"
        >
          <div className="approval-confirm">
            <h3>确认执行以下方案？</h3>
            <dl className="confirm-detail">
              <dt>目标</dt>
              <dd>
                {target.kind}/{target.name}（{target.namespace}）
              </dd>
              <dt>动作</dt>
              <dd>{proposal?.action ?? "—"}</dd>
              <dt>参数</dt>
              <dd className="mono">
                {proposal?.parameters
                  ? JSON.stringify(proposal.parameters)
                  : "—"}
              </dd>
              <dt>固有风险</dt>
              <dd>{proposal?.risk ?? "未声明"}</dd>
              <dt>摘要</dt>
              <dd className="mono">{proposal?.planDigest ?? "—"}</dd>
            </dl>
            <div className="confirm-actions">
              <button
                type="button"
                className="btn-approve"
                onClick={() => {
                  setConfirming(false);
                  void submit("Approve");
                }}
                disabled={submitting}
              >
                确认批准
              </button>
              <button
                type="button"
                className="btn-cancel"
                onClick={() => setConfirming(false)}
                disabled={submitting}
              >
                取消
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

export default ApprovalActions;
