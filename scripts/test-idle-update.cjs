// Deterministic policy checks; no browser dependencies or household data.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../web/static/update.js'), 'utf8');
const oldVersion = 'a'.repeat(24), newVersion = 'b'.repeat(24);
function fixture() {
  const f = {now: 0, reloads: 0, requests: 0, version: oldVersion, blocked: false,
    hidden: false, focused: false, status: 200, fails: false, held: false};
  const events = {}, timers = [];
  function on(name, handler) { (events[name] ||= []).push(handler); }
  f.emit = (name, target = {closest: () => null}, extra = {}) => {
    for (const h of events[name] || []) h({target, ...extra});
  };
  const document = {
    body: {dataset: {appVersion: oldVersion}},
    get hidden() {return f.hidden;},
    get activeElement() {return {matches: () => f.focused};},
    querySelector: selector => {
      f.selector = selector;
      return f.blocked ? {} : null;
    },
    addEventListener: on,
  };
  vm.runInNewContext(source, {
    document, window: {addEventListener: on}, performance: {now: () => f.now},
    location: {reload: () => f.reloads++}, AbortController,
    setInterval: (handler, ms) => {timers.push({handler, ms});},
    setTimeout: () => 1, clearTimeout: () => {},
    fetch: async (url, options) => {
      f.requests++;
      assert.equal(url, '/version'); assert.equal(options.cache, 'no-store');
      assert.equal(options.redirect, 'error'); assert.equal(options.credentials, 'same-origin');
      if (f.fails) throw new Error('offline');
      return {ok: f.status === 200, json: async () => ({version: f.version})};
    },
  });
  f.tick = async ms => {
    for (const t of timers.filter(t => t.ms === ms)) t.handler();
    for (let i=0;i<10;i++) await Promise.resolve();
  };
  return f;
}
(async () => {
  let f = fixture(); f.now = 120000; await f.tick(60000);
  assert.equal(f.reloads, 0, 'same version must not reload');
  f.version = newVersion; await f.tick(60000);
  assert.equal(f.reloads, 1, 'idle version change reloads');
  await f.tick(5000); assert.equal(f.reloads, 1, 'no duplicate reload');

  f = fixture(); f.version = newVersion; f.now = 60000;
  f.emit('pointerdown'); f.emit('pointerup'); await f.tick(60000);
  assert.equal(f.reloads, 0, 'recent touch waits');
  f.now = 119999; await f.tick(5000); assert.equal(f.reloads, 0);
  f.now = 120000; await f.tick(5000); assert.equal(f.reloads, 1);

  for (const event of ['keydown', 'wheel', 'touchmove', 'input', 'change']) {
    f = fixture(); f.version = newVersion; f.now = 60000; f.emit(event);
    await f.tick(60000); assert.equal(f.reloads, 0, event+' counts as activity');
  }
  f = fixture(); f.version = newVersion; f.emit('pointerdown'); f.now = 120000;
  await f.tick(60000); assert.equal(f.reloads, 0, 'held pointer must block');

  f = fixture(); f.version = newVersion; f.now = 120000; f.blocked = true;
  await f.tick(60000); assert.equal(f.reloads, 0, 'protected content waits');
  for (const selector of ['.sheet', '.entry-form', '.recipe-shopping-form', '[data-cooking]', 'form[data-unsaved]', '.htmx-request']) {
    assert.ok(f.selector.includes(selector), 'missing protected state '+selector);
  }
  f.blocked = false; f.focused = true; await f.tick(5000);
  assert.equal(f.reloads, 0, 'focused input waits');
  f.focused = false; await f.tick(5000); assert.equal(f.reloads, 1);

  let dirty = false;
  f = fixture(); f.emit('input', {closest: () => ({setAttribute: name => {dirty = name === 'data-unsaved';}})});
  assert.equal(dirty, true, 'unsaved input must be marked');

  f = fixture(); f.version = newVersion; f.now = 120000; f.blocked = true;
  await f.tick(60000); f.blocked = false; f.fails = true; await f.tick(5000);
  assert.equal(f.reloads, 0, 'queued update must not reload during outage');
  f.fails = false; f.status = 503; await f.tick(5000); assert.equal(f.reloads, 0);
  f.status = 200; f.version = 'invalid'; await f.tick(5000); assert.equal(f.reloads, 0);
  f.version = oldVersion; await f.tick(5000); assert.equal(f.reloads, 0, 'rollback cancels pending update');
  const requests = f.requests; await f.tick(5000); assert.equal(f.requests, requests);

  f = fixture(); f.version = newVersion; f.now = 120000; f.hidden = true;
  await f.tick(60000); assert.equal(f.requests, 0, 'hidden tab does not poll');
  f.hidden = false; f.emit('visibilitychange');
  for(let i=0;i<10;i++) await Promise.resolve();
  assert.equal(f.reloads, 0, 'foreground resumes with a fresh idle period');
  f.now += 60000; await f.tick(60000); assert.equal(f.reloads, 1);
  console.log('PASS: idle reload, interaction/editor/cooking guards, fresh recheck, outages, rollback and foreground recovery.');
})().catch(error => {console.error(error); process.exitCode = 1;});
