import { useQuery } from "@tanstack/react-query";
import type { EvidenceResponse, TimelineResponse } from "../api/types";
import { fetchIncidentEvidence, fetchIncidentTimeline } from "../api/incidents";

export function useIncidentTimeline(namespace: string, name: string) {
  return useQuery({
    queryKey: ["incident-timeline", namespace, name],
    queryFn: () => fetchIncidentTimeline(namespace, name),
    enabled: Boolean(namespace && name),
    staleTime: 15_000,
  });
}

export function useIncidentEvidence(namespace: string, name: string) {
  return useQuery({
    queryKey: ["incident-evidence", namespace, name],
    queryFn: () => fetchIncidentEvidence(namespace, name),
    enabled: Boolean(namespace && name),
    staleTime: 30_000,
  });
}

export type { TimelineResponse, EvidenceResponse };
