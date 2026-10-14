// Connection policy checks use no household data or browser dependencies.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../web/static/connection.js'), 'utf8');
function fixture() {
  const f = {now:0, banner:{hidden:true}, message:{textContent:''}}, events = {};
  vm.runInNewContext(source, {performance:{now:()=>f.now}, document:{
    getElementById:id=>id==='connection'?f.banner:f.message,
    addEventListener:(name,handler)=>events[name]=handler,
  }});
  f.send = (detail, name='htmx:afterRequest')=>events[name]({detail});
  f.poll = (extra={})=>({xhr:{}, failed:true, elt:{id:'clock',hasAttribute:()=>false}, ...extra});
  return f;
}
let f=fixture();
f.send(f.poll()); assert.equal(f.banner.hidden,true,'one failed poll stays quiet');
f.now=30000; f.send(f.poll()); assert.equal(f.banner.hidden,true,'two failures within grace stay quiet');
f.now=59999; f.send(f.poll()); assert.equal(f.banner.hidden,true,'grace lasts a full minute');
f.now=60000; f.send(f.poll()); assert.equal(f.banner.hidden,false,'persistent outage warns');
assert.match(f.message.textContent,/Retrying automatically/);
f.send({xhr:{},successful:true}); assert.equal(f.banner.hidden,true,'recovery clears warning');
f.now=120000;f.send(f.poll());assert.equal(f.banner.hidden,true,'recovery resets the grace period');

for(const detail of [
 {elt:{id:'appearance-refresh',hasAttribute:()=>false}},
 {elt:{id:'',hasAttribute:name=>name==='data-media-poll'}},
 {requestConfig:{headers:{'X-Home-HQ-Background':'true'}}},
]) {
 f=fixture(); f.send(f.poll(detail)); assert.equal(f.banner.hidden,true,'all background paths get grace');
 f.now=60000;f.send(f.poll(detail));assert.equal(f.banner.hidden,false);
}
for(const name of ['htmx:sendError','htmx:timeout']) {
 f=fixture();f.send(f.poll({failed:undefined}),name);assert.equal(f.banner.hidden,true);
 f.now=60000;f.send(f.poll({failed:undefined}),name);assert.equal(f.banner.hidden,false,'network failures participate');
}
f=fixture();const duplicate=f.poll();f.send(duplicate);f.now=60000;f.send(duplicate);
assert.equal(f.banner.hidden,true,'duplicate HTMX notifications are not repeated failures');
f.send(f.poll());assert.equal(f.banner.hidden,false);
f=fixture();f.send({xhr:{},elt:{id:'clock',hasAttribute:()=>false}});f.now=60000;f.send(f.poll());
assert.equal(f.banner.hidden,true,'canceled/aborted requests do not start an outage');
for(const verb of ['get','post']) {
 f=fixture();f.send({xhr:{},failed:true,requestConfig:{verb}});
 assert.equal(f.banner.hidden,false,'user requests fail immediately');
 assert.match(f.message.textContent,/Check the result/);
 f.send({xhr:{},successful:true});assert.equal(f.banner.hidden,true);
}
// Reading this policy registers only result listeners: no automatic POST retry.
assert.ok(!source.includes('htmx.ajax'));
console.log('PASS: transient polls, one-minute grace, recovery, background paths, network errors/timeouts, duplicate events, canceled requests and immediate user failures.');
