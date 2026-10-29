import assert from 'node:assert/strict';
import test from 'node:test';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { createLatestDraftPreview } = require('../static/draft-preview.js');

test('older failed previews cannot replace a newer successful preview', async () => {
    const request = createLatestDraftPreview();
    let rejectOlder;
    let preview = '';
    let error = '';
    const older = request.run(
        () => new Promise((_, reject) => { rejectOlder = reject; }),
        value => { preview = value; },
        value => { error = value.message; },
    );
    const newer = request.run(
        () => Promise.resolve('current patch'),
        value => { preview = value; },
        value => { error = value.message; },
    );

    await newer;
    rejectOlder(new Error('stale request failed'));
    await older;

    assert.equal(preview, 'current patch');
    assert.equal(error, '');
});

test('invalidating a preview prevents stale results after edits', async () => {
    const request = createLatestDraftPreview();
    let resolveRequest;
    let preview = '';
    const pending = request.run(
        () => new Promise(resolve => { resolveRequest = resolve; }),
        value => { preview = value; },
        () => assert.fail('invalidated failures must not update the page'),
    );

    request.invalidate();
    resolveRequest('stale patch');
    await pending;

    assert.equal(preview, '');
});
