/**
 * The playdag wire contract, restated in TypeScript. This file is the only part
 * of the adapter that the orchestrator constrains; everything else here is
 * Playwright's business. See docs/PROTOCOL.md for the specification.
 */

export const SCHEMA_VERSION = '1.1';

/** Versions this adapter accepts from the orchestrator. */
const SUPPORTED_VERSIONS = ['1.0', SCHEMA_VERSION];

/** Environment variable carrying the encoded request. */
export const CONTEXT_ENV = 'PLAYDAG_CONTEXT';

/** Environment variable naming the file the response is written to. */
export const RESULT_ENV = 'PLAYDAG_RESULT';

export type Status = 'PASSED' | 'FAILED' | 'SKIPPED';

export interface PlaydagRequest {
  schema_version: string;
  test_id: string;
  script?: string;
  attempt: number;
  base_url?: string;
  requires?: string[];
  provides?: string[];
  context?: Record<string, string>;
}

export interface PlaydagArtifact {
  type: string;
  path: string;
}

export interface PlaydagResponse {
  schema_version: string;
  status: Status;
  provides?: Record<string, string>;
  error?: string;
  artifacts?: PlaydagArtifact[];
}

export interface PlaydagBatchResponse {
  schema_version: string;
  results: Record<string, PlaydagResponse>;
}

/** What the orchestrator asked this process to run. */
export interface Envelope {
  /** True when the orchestrator sent several nodes at once. */
  batch: boolean;
  tests: PlaydagRequest[];
}

/**
 * Reads what the orchestrator passed in: one node, a batch of them, or nothing
 * at all when the tests are run directly by Playwright. That last case is what
 * lets the same spec files be debugged with `npx playwright test`.
 */
export function readEnvelope(env: NodeJS.ProcessEnv = process.env): Envelope | null {
  const raw = env[CONTEXT_ENV];
  if (!raw) return null;

  let parsed: { schema_version?: string; tests?: PlaydagRequest[] };
  try {
    parsed = JSON.parse(raw);
  } catch (e) {
    throw new Error(`playdag: ${CONTEXT_ENV} is not valid JSON: ${(e as Error).message}`);
  }
  if (!SUPPORTED_VERSIONS.includes(parsed.schema_version ?? '')) {
    throw new Error(
      `playdag: unsupported schema_version ${JSON.stringify(parsed.schema_version)} ` +
        `(want one of ${SUPPORTED_VERSIONS.join(', ')})`,
    );
  }

  if (Array.isArray(parsed.tests)) {
    return { batch: true, tests: parsed.tests };
  }
  return { batch: false, tests: [parsed as PlaydagRequest] };
}

/**
 * The request for one test, matched by node id. A batch process runs several
 * tests and each must see only its own context — handing a test the whole batch
 * would defeat the contract that says what it is allowed to read.
 */
export function requestFor(testId: string | undefined, env?: NodeJS.ProcessEnv): PlaydagRequest | null {
  const envelope = readEnvelope(env);
  if (!envelope) return null;
  if (!envelope.batch) return envelope.tests[0] ?? null;
  return envelope.tests.find((t) => t.test_id === testId) ?? null;
}
