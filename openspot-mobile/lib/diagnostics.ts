export type DiagnosticCategory = 'provider' | 'playback' | 'automix' | 'extension' | 'cache';

export interface DiagnosticEvent {
  id: string;
  at: number;
  category: DiagnosticCategory;
  type: string;
  message?: string;
  data?: Record<string, unknown>;
}

const MAX_DIAGNOSTIC_EVENTS = 240;
const events: DiagnosticEvent[] = [];
const listeners = new Set<(event: DiagnosticEvent) => void>();
let sequence = 0;

export function recordDiagnostic(event: Omit<DiagnosticEvent, 'id' | 'at'>): DiagnosticEvent {
  const recorded: DiagnosticEvent = {
    ...event,
    id: `${Date.now()}-${sequence += 1}`,
    at: Date.now(),
  };
  events.push(recorded);
  if (events.length > MAX_DIAGNOSTIC_EVENTS) {
    events.splice(0, events.length - MAX_DIAGNOSTIC_EVENTS);
  }
  for (const listener of listeners) listener(recorded);
  return recorded;
}

export function getDiagnostics(limit = MAX_DIAGNOSTIC_EVENTS): DiagnosticEvent[] {
  const boundedLimit = Math.max(0, Math.min(limit, MAX_DIAGNOSTIC_EVENTS));
  if (boundedLimit === 0) return [];
  return events.slice(-boundedLimit);
}

export function clearDiagnostics(): void {
  events.length = 0;
}

export function subscribeDiagnostics(listener: (event: DiagnosticEvent) => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
