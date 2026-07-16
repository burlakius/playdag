/**
 * Test metadata declared in the test file itself. The orchestrator selects a
 * node by its `@id:` tag; the annotations describe the node's data contract and
 * are what the metadata extractor reads to generate a suite.
 */

export const ID_ANNOTATION = 'playdag:id';
export const DESCRIPTION_ANNOTATION = 'playdag:description';
export const REQUIRES_ANNOTATION = 'playdag:requires';
export const PROVIDES_ANNOTATION = 'playdag:provides';
export const CRITICALITY_ANNOTATION = 'playdag:criticality';
export const PRIORITY_ANNOTATION = 'playdag:priority';
export const TAGS_ANNOTATION = 'playdag:tags';
export const RETRIES_ANNOTATION = 'playdag:retries';
export const TIMEOUT_ANNOTATION = 'playdag:timeout';
export const RETRY_DELAY_ANNOTATION = 'playdag:retry_delay';
export const BACKOFF_ANNOTATION = 'playdag:backoff';
export const SCOPE_ANNOTATION = 'playdag:scope';

/** Prefix of the annotation a test writes when it provides a value at runtime. */
export const PROVIDE_ANNOTATION_PREFIX = 'playdag:provide:';

export type Criticality = 'BLOCKER' | 'NON_CRITICAL';

/** How long a value provided by the node stays in the context. */
export type Scope = 'session' | 'suite' | 'global';

/** How the delay between retries grows: constant, or doubling each attempt. */
export type Backoff = 'fixed' | 'exponential';

export interface PlaydagMeta {
  /** Node identifier, unique across the suite. */
  id: string;
  /** What the node does, for the report. */
  description?: string;
  /** Context keys the test needs before it can run. */
  requires?: string[];
  /** Context keys the test writes for its dependants. */
  provides?: string[];
  criticality?: Criticality;
  priority?: number;
  /** How many times the node is retried before it counts as failed. */
  retries?: number;
  /** Per-node timeout as a Go duration, for example '30s'. */
  timeout?: string;
  /** Wait before a retry, as a Go duration; retrying instantly rarely helps. */
  retryDelay?: string;
  backoff?: Backoff;
  scope?: Scope;
  /** Resource tags, matched against the agent pool. */
  tags?: string[];
}

export interface Annotation {
  type: string;
  description: string;
}

/**
 * Turns node metadata into Playwright test options: the `@id:` tag the
 * orchestrator greps for, plus the annotations that describe the contract.
 *
 *   test('create card', playdagMeta({ id: 'card.create', requires: ['session.token'],
 *     provides: ['card.id'] }), async ({ playdag }) => { ... });
 */
export function playdagMeta(meta: PlaydagMeta): { tag: string; annotation: Annotation[] } {
  const annotation: Annotation[] = [{ type: ID_ANNOTATION, description: meta.id }];

  if (meta.description) {
    annotation.push({ type: DESCRIPTION_ANNOTATION, description: meta.description });
  }
  if (meta.requires?.length) {
    annotation.push({ type: REQUIRES_ANNOTATION, description: meta.requires.join(',') });
  }
  if (meta.provides?.length) {
    annotation.push({ type: PROVIDES_ANNOTATION, description: meta.provides.join(',') });
  }
  if (meta.criticality) {
    annotation.push({ type: CRITICALITY_ANNOTATION, description: meta.criticality });
  }
  if (meta.priority !== undefined) {
    annotation.push({ type: PRIORITY_ANNOTATION, description: String(meta.priority) });
  }
  if (meta.retries !== undefined) {
    annotation.push({ type: RETRIES_ANNOTATION, description: String(meta.retries) });
  }
  if (meta.timeout) {
    annotation.push({ type: TIMEOUT_ANNOTATION, description: meta.timeout });
  }
  if (meta.retryDelay) {
    annotation.push({ type: RETRY_DELAY_ANNOTATION, description: meta.retryDelay });
  }
  if (meta.backoff) {
    annotation.push({ type: BACKOFF_ANNOTATION, description: meta.backoff });
  }
  if (meta.scope) {
    annotation.push({ type: SCOPE_ANNOTATION, description: meta.scope });
  }
  if (meta.tags?.length) {
    annotation.push({ type: TAGS_ANNOTATION, description: meta.tags.join(',') });
  }

  return { tag: idTag(meta.id), annotation };
}

/** The tag the orchestrator greps for when it runs a single node. */
export function idTag(id: string): string {
  return `@id:${id}`;
}
