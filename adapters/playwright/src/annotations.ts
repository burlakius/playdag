/**
 * Test metadata declared in the test file itself. The orchestrator selects a
 * node by its `@id:` tag; the annotations describe the node's data contract and
 * are what the metadata extractor reads to generate a suite.
 */

export const ID_ANNOTATION = 'playdag:id';
export const REQUIRES_ANNOTATION = 'playdag:requires';
export const PROVIDES_ANNOTATION = 'playdag:provides';
export const CRITICALITY_ANNOTATION = 'playdag:criticality';
export const PRIORITY_ANNOTATION = 'playdag:priority';
export const TAGS_ANNOTATION = 'playdag:tags';

/** Prefix of the annotation a test writes when it provides a value at runtime. */
export const PROVIDE_ANNOTATION_PREFIX = 'playdag:provide:';

export type Criticality = 'BLOCKER' | 'NON_CRITICAL';

export interface PlaydagMeta {
  /** Node identifier, unique across the suite. */
  id: string;
  /** Context keys the test needs before it can run. */
  requires?: string[];
  /** Context keys the test writes for its dependants. */
  provides?: string[];
  criticality?: Criticality;
  priority?: number;
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
  if (meta.tags?.length) {
    annotation.push({ type: TAGS_ANNOTATION, description: meta.tags.join(',') });
  }

  return { tag: idTag(meta.id), annotation };
}

/** The tag the orchestrator greps for when it runs a single node. */
export function idTag(id: string): string {
  return `@id:${id}`;
}
