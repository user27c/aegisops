import type { TimelineEntry } from "../api/types";
import EmptyState from "./EmptyState";

interface AuditTimelineProps {
  items?: TimelineEntry[];
  source?: "audit" | "cr";
  detailsUnavailable?: boolean;
}

function AuditTimeline({
  items,
  source,
  detailsUnavailable,
}: AuditTimelineProps) {
  if (detailsUnavailable && (!items || items.length === 0)) {
    return (
      <p className="notice" role="status">
        审计时间线暂不可用，显示 Incident 内记录。
      </p>
    );
  }

  if (!items || items.length === 0) {
    return <EmptyState message="暂无时间线" />;
  }

  return (
    <div className="audit-timeline-wrapper">
      {detailsUnavailable && (
        <p className="notice" role="status">
          审计时间线暂不可用，显示 Incident 内记录。
        </p>
      )}
      <ol className="audit-timeline-list" aria-label="审计事件时间线">
        {items.map((entry, idx) => (
          <li key={entry.sequence ?? idx} className="audit-timeline-node">
            <div className="timeline-node-marker" aria-hidden="true">
              <span className="node-dot" />
              {idx < items.length - 1 && <span className="node-line" />}
            </div>
            <div className="timeline-node-content">
              <div className="timeline-node-header">
                <span className="timeline-seq">
                  {entry.sequence !== undefined ? `#${entry.sequence}` : `#${idx + 1}`}
                </span>
                <span className="timeline-time mono">
                  {new Date(entry.time).toLocaleTimeString()}
                </span>
                <span className="timeline-type">{entry.type}</span>
                {entry.actor && (
                  <span className="timeline-actor">@{entry.actor}</span>
                )}
              </div>
              {entry.message && (
                <p className="timeline-message">{entry.message}</p>
              )}
              {entry.eventHash && (
                <div className="timeline-hash-row">
                  <span className="hash-label">Event Hash:</span>
                  <code className="mono">#{entry.eventHash}</code>
                </div>
              )}
            </div>
          </li>
        ))}
      </ol>
      {source && (
        <div className="timeline-footer">
          <span>来源: {source === "audit" ? "诊断审计" : "Incident（降级）"}</span>
        </div>
      )}
    </div>
  );
}

export default AuditTimeline;


