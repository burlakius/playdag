import { writeFileSync } from 'node:fs';
import type { Reporter, TestCase, TestResult } from '@playwright/test/reporter';
import { ID_ANNOTATION, PROVIDE_ANNOTATION_PREFIX } from './annotations';
import { readEnvelope, RESULT_ENV, SCHEMA_VERSION } from './protocol';
import type { PlaydagArtifact, PlaydagResponse, Status } from './protocol';

const ARTIFACT_TYPES = ['trace', 'video', 'screenshot'];

const NOT_RUN = 'playdag: no test matched the requested node';

/** Maps Playwright's outcome onto a protocol status. */
function toStatus(result: TestResult): Status {
  switch (result.status) {
    case 'passed':
      return 'PASSED';
    case 'skipped':
      return 'SKIPPED';
    default:
      return 'FAILED';
  }
}

/**
 * Annotations pushed while a test runs surface on the result in current
 * Playwright versions and on the test case in older ones.
 */
function annotationsOf(test: TestCase, result: TestResult): { type: string; description?: string }[] {
  const fromResult = (result as { annotations?: { type: string; description?: string }[] }).annotations;
  return fromResult ?? test.annotations;
}

function providesOf(test: TestCase, result: TestResult): Record<string, string> {
  const provides: Record<string, string> = {};
  for (const a of annotationsOf(test, result)) {
    if (a.type.startsWith(PROVIDE_ANNOTATION_PREFIX)) {
      provides[a.type.slice(PROVIDE_ANNOTATION_PREFIX.length)] = a.description ?? '';
    }
  }
  return provides;
}

function nodeIdOf(test: TestCase, result: TestResult): string | undefined {
  return annotationsOf(test, result).find((a) => a.type === ID_ANNOTATION)?.description;
}

function artifactsOf(result: TestResult): PlaydagArtifact[] {
  const artifacts: PlaydagArtifact[] = [];
  for (const att of result.attachments) {
    if (!att.path) continue;
    const type = ARTIFACT_TYPES.find((t) => att.name.includes(t)) ?? 'log';
    artifacts.push({ type, path: att.path });
  }
  return artifacts;
}

function responseOf(test: TestCase, result: TestResult): PlaydagResponse {
  return {
    schema_version: SCHEMA_VERSION,
    status: toStatus(result),
    provides: providesOf(test, result),
    error: result.error?.message ?? '',
    artifacts: artifactsOf(result),
  };
}

/**
 * Reports node outcomes to the orchestrator in the playdag protocol.
 *
 * The response goes to the file named by PLAYDAG_RESULT, keeping it clear of
 * everything Playwright prints on stdout. Its shape mirrors the request: a
 * single node answers with one response, a batch answers with a map keyed by
 * node id, and every node asked for is answered - a node that never ran is
 * reported as such rather than left out.
 */
export default class PlaydagReporter implements Reporter {
  private readonly requested = readEnvelope();
  private readonly results = new Map<string, PlaydagResponse>();
  private seen = 0;

  onBegin(): void {
    for (const req of this.requested?.tests ?? []) {
      this.results.set(req.test_id, { schema_version: SCHEMA_VERSION, status: 'FAILED', error: NOT_RUN });
    }
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    this.seen += 1;
    const id = nodeIdOf(test, result);
    if (id !== undefined) {
      this.results.set(id, responseOf(test, result));
      return;
    }
    // A test with no id tag can only be matched positionally, and only when a
    // single node was asked for.
    const only = this.requested?.tests[0]?.test_id;
    if (only !== undefined && !this.requested?.batch) {
      this.results.set(only, responseOf(test, result));
    }
  }

  onEnd(): void {
    const payload = JSON.stringify(this.envelope());
    const path = process.env[RESULT_ENV];
    if (!path) {
      process.stdout.write(payload + '\n');
      return;
    }
    writeFileSync(path, payload);
  }

  private envelope(): unknown {
    if (this.requested?.batch) {
      return { schema_version: SCHEMA_VERSION, results: Object.fromEntries(this.results) };
    }

    const only = this.requested?.tests[0]?.test_id;
    if (only !== undefined && this.seen > 1) {
      return {
        schema_version: SCHEMA_VERSION,
        status: 'FAILED',
        error: `playdag: ${this.seen} tests matched one node; an @id: tag must select exactly one test`,
      };
    }
    return (
      (only !== undefined ? this.results.get(only) : undefined) ?? {
        schema_version: SCHEMA_VERSION,
        status: 'FAILED',
        error: NOT_RUN,
      }
    );
  }
}
