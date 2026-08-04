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
  if (detailsUnavailable) {
    return (
      <>
        <p className="notice" role="status">
          审计时间线暂不可用，显示 Incident 内记录。
        </p>
        {items && items.length > 0 && (
          <ul className="timeline">
            {items.map((entry, idx) => (
              <li key={idx}>
                <span className="mono">
                  {new Date(entry.time).toLocaleTimeString()}
                </span>{" "}
                {entry.type}
                {entry.actor && (
                  <span className="timeline-actor"> @{entry.actor}</span>
                )}
                {entry.sequence !== undefined && (
                  <span className="timeline-sequence">
                    {" "}
                    seq={entry.sequence}
                  </span>
                )}
                {entry.message && (
                  <span className="timeline-message"> — {entry.message}</span>
                )}
                {entry.eventHash && (
                  <span className="mono timeline-hash">
                    {" "}
                    #{entry.eventHash}
                  </span>
                )}
              </li>
            ))}
            <li className="timeline-source">来源: Incident（降级）</li>
          </ul>
        )}
      </>
    );
  }
  if (!items || items.length === 0) {
    return <EmptyState message="暂无时间线" />;
  }
  return (
    <ul className="timeline">
      {items.map((entry, idx) => (
        <li key={idx}>
          <span className="mono">
            {new Date(entry.time).toLocaleTimeString()}
          </span>{" "}
          {entry.type}
          {entry.actor && (
            <span className="timeline-actor"> @{entry.actor}</span>
          )}
          {entry.sequence !== undefined && (
            <span className="timeline-sequence"> seq={entry.sequence}</span>
          )}
          {entry.message && (
            <span className="timeline-message"> — {entry.message}</span>
          )}
          {entry.eventHash && (
            <span className="mono timeline-hash"> #{entry.eventHash}</span>
          )}
        </li>
      ))}
      {source && (
        <li className="timeline-source">
          来源: {source === "audit" ? "诊断审计" : "Incident"}
        </li>
      )}
    </ul>
  );
}

export default AuditTimeline;
