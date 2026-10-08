'use strict';

// Standalone real-browser fixture. No product server, router, or npm install.
// Set PLAYWRIGHT_MODULE_PATH when Playwright is installed outside the repository.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');
const staticRoot = path.join(__dirname, '../internal/web/ui/static');
const screenshots = fs.mkdtempSync('/tmp/krm-ui-browser-');
const permissions = ['vpn.view','vpn.control','subscriptions.view','subscriptions.manage','router.view','router.clients','router.policy','router.wake','router.system','router.reboot','updates.manage','users.manage','config.manage','events.view'];
function sourceFixture() { return [{id:'primary',name:'Primary',url:'https://subscription.example.invalid/private-fixture',enabled:true,headers:{Authorization:'Bearer synthetic-fixture'}}]; }
function usersFixture() { return [{id:'fixture-admin',username:'admin',enabled:true,permissions},{id:'fixture-user',username:'operator',enabled:true,permissions:['vpn.view'],updated_at:'fixture-user-original'}]; }
function settingsFixture() {
  return {
    benchmark: { full_interval:'5m',batch_size:20,latency_workers:8,requests_per_weight:2,finalists:6,min_improvement_percent:15,switch_cooldown:'10m',stability_before_upgrade:'10m',speed:{enabled:false,workers:2,url_template:'https://speed.example.invalid/?bytes={bytes}',warmup_bytes:'8 MiB',min_sample_bytes:'64 MiB',max_sample_bytes:'512 MiB',target_duration:'6s',repetitions:3} },
    health:{recovery_threshold:2,request_timeout:'8s',max_response_bytes:'64 KiB',hot_pool_freshness:'5m',provider_retry_backoff:['15s','30s','1m','2m','5m','10m']},
    failover:{detection_interval:'5s',failure_threshold:2,probe_timeout:'2s',overall_deadline:'5s',quorum:2},
    subscriptions:{cache_enabled:false,cache_ttl:'168h',refresh_interval:'30m',request_timeout:'20s',max_response_bytes:'4 MiB',max_nodes_per_source:500,max_sources:20,max_nodes:500},
    pool:{provider_diversity:{enabled:false,max_per_provider:1}},
  };
}
const state = { permissions:[...permissions], logged:true, revision:'fixture-original', settings:settingsFixture(), policyVersion:0, requests:[], saves:[], panelStatus:'idle', sources:sourceFixture(),users:usersFixture(),activeSlot:0 };
const now = () => new Date().toISOString();
const metrics = () => ({cpu_percent:24,ram_percent:38,wan_connected:true,wan_name:'ISP',wan_ip:'192.0.2.10',wan_description:'Ethernet',traffic_available:true,rx_mbps:18.4,tx_mbps:2.1,updated_at:now(),temperature_c:42,uptime_seconds:86410,ports:[{id:'LAN 1',link:'up',speed:'1000 Мбит/с'}]});
const nodes = [
  {id:'zero',label:'Zero latency',sources:['Primary'],active:true,in_pool:true,network:'tcp',security:'reality',measurement:{latency_ms:0,speed_mbps:0,score:0,healthy:true,checked_at:now()}},
  {id:'known',label:'Known node',sources:['Backup'],network:'ws',security:'tls',measurement:{latency_ms:42,speed_mbps:76,score:100,healthy:false,checked_at:now()}},
  {id:'missing',label:'Unchecked node',sources:['Backup'],network:'tcp',security:'tls',measurement:{latency_ms:null,speed_mbps:null,score:null}},
];
function fakeAPI(url,method,body) {
  const route = url.pathname; state.requests.push({route,method,body});
  if(route === '/api/v1/auth/login') { state.logged=true; return {csrf:'fixture-csrf',permissions:state.permissions,user:{id:'fixture-admin',username:'admin'}}; }
  if(route === '/api/v1/auth/logout') { state.logged=false; return {}; }
  if(!state.logged) return {error:'unauthorized',httpStatus:401};
  if(route === '/api/v1/session') return {csrf:'fixture-csrf',permissions:state.permissions,user:{id:'fixture-admin',username:'admin'}};
  if(route === '/api/v1/status') return {version:'1.3.1-fixture',xray_running:true,capabilities:{client_policy:true,wake_on_lan:true},state:{xray_configured:true,automatic_routing_paused:false,direct_mode:false,active_slot:state.activeSlot,active_node_id:state.activeSlot?'known':'zero',health_status:'healthy',pool:[{index:0,node_id:'zero',label:'Zero latency',score:0},{index:1,node_id:'known',label:'Known node',score:100}],sources:{Primary:{name:'Primary',status:'healthy',node_count:3}},last_benchmark:{finished_at:now()}}};
  if(route === '/api/v1/nodes') return nodes;
  if(route === '/api/v1/router/clients') return {updated_at:now(),value:[{name:`Workstation ${state.policyVersion}`,mac:'02:00:00:00:00:01',ip:'192.168.1.2',active:true,connection_policy:state.policyVersion%2?'Default route':'XKeen',link:'LAN'}]};
  if(route === '/api/v1/router/metrics') return metrics();
  if(route === '/api/v1/router/metrics/history') return {samples:[1200000,180000,175000,50000,45000].map((age,i)=>({...metrics(),cpu_percent:i*10,ram_percent:40,updated_at:new Date(Date.now()-age).toISOString()}))};
  if(route === '/api/v1/subscriptions') return {sources:state.sources};
  if(route === '/api/v1/subscriptions/save') {const source=JSON.parse(body);state.sources=state.sources.filter(item=>item.id!==source.id).concat(source);return {ok:true};}
  if(route === '/api/v1/subscriptions/delete') {state.sources=state.sources.filter(item=>item.id!==JSON.parse(body).id);return {ok:true};}
  if(route === '/api/v1/users') return {users:state.users,permissions,roles:{admin:permissions,viewer:['vpn.view','router.view']}};
  if(route === '/api/v1/users/save') {const user=JSON.parse(body);state.users=state.users.filter(item=>item.id!==user.id).concat({...user,updated_at:'fixture-updated'});return {ok:true};}
  if(route === '/api/v1/users/delete') {state.users=state.users.filter(item=>item.id!==JSON.parse(body).id);return {ok:true};}
  if(route === '/api/v1/actions/switch') {state.activeSlot=JSON.parse(body).index;return {accepted:true,httpStatus:202};}
  if(route === '/api/v1/events') return [];
  if(route === '/api/v1/update/status') return {enabled:true,launcher:true,channel:'stable',channel_switch_supported:true,phase:'idle',current_version:'1.3.1-fixture'};
  if(route === '/api/v1/settings') return {settings:state.settings,revision:state.revision,pool_size:5,apply:{status:'applied',revision:state.revision}};
  if(route === '/api/v1/settings/validate') return {valid:true};
  if(route === '/api/v1/settings/save') { const input=JSON.parse(body); state.settings=input.settings;state.revision='fixture-saved';state.saves.push(input);return {changed:true,accepted:true,revision:state.revision,httpStatus:202}; }
  if(route === '/api/v1/panel/status') return {...(state.panelStatus==='awaiting_confirmation'?{revision:'fixture-panel-trial',confirmation_deadline:new Date(Date.now()+180000).toISOString()}:{}),supported:true,hostname:'alice.jopa',port:443,listen_ip:'192.168.1.1',url:'https://alice.jopa',status:state.panelStatus,certificate_changed:false,dns_automatic:true};
  if(route.startsWith('/api/v1/actions/')) return {accepted:true,httpStatus:202};
  return {};
}
const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if(url.pathname === '/favicon.ico') {res.writeHead(204);res.end();return;}
  if(url.pathname.startsWith('/api/')) { let body='';for await(const chunk of req)body+=chunk;const result=fakeAPI(url,req.method,body);const status=result.httpStatus||200;delete result.httpStatus;res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(result));return; }
  const name=url.pathname==='/'?'index.html':url.pathname.startsWith('/assets/')?url.pathname.slice(8):url.pathname.slice(1);
  if(!/^[a-z-]+\.(?:js|css|webmanifest|html)$/.test(name)) {res.writeHead(404);res.end();return;}
  try { const data=fs.readFileSync(path.join(staticRoot,name));res.writeHead(200,{'Content-Type':name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.html')?'text/html':'application/manifest+json','Cache-Control':'no-store'});res.end(data); } catch {res.writeHead(404);res.end();}
});
async function navigate(page,name,width) {
  if(width<761) await page.locator('#nav-toggle').click();
  const group=['overview','subscriptions','nodes','testing','events'].includes(name)?'vpn':'router';
  const toggle=page.locator(`.group-toggle[data-group="${group}"]`);
  if(await toggle.getAttribute('aria-expanded')!=='true') await toggle.click();
  await page.locator(`[data-tab="${name}"]`).click();
  await page.locator(`#tab-${name}`).waitFor({state:'visible'});
  assert(await page.evaluate(()=>{const el=document.activeElement;return el===document.body||(!el.closest('.hidden')&&!el.closest('[inert]')&&el.getClientRects().length>0);}), 'navigation focused a hidden/inert control');
}
async function noOverflow(page,label) {
  const sizes=await page.evaluate(()=>({body:document.documentElement.scrollWidth,viewport:innerWidth}));
  assert(sizes.body<=sizes.viewport+1,`${label}: horizontal page overflow ${JSON.stringify(sizes)}`);
}
async function main() {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const origin=`http://127.0.0.1:${server.address().port}`;
  const browser=await chromium.launch({headless:true});
  try {
    for(const width of [1440,390,320]) for(const scheme of ['light','dark']) {
      state.logged=true;state.permissions=[...permissions];state.settings=settingsFixture();state.revision='fixture-original';state.policyVersion=0;state.panelStatus='idle';state.sources=sourceFixture();state.users=usersFixture();state.activeSlot=0;
      const context=await browser.newContext({viewport:{width,height:width<761?844:1000},colorScheme:scheme,serviceWorkers:'block'});
      const page=await context.newPage(); const errors=[];
      page.on('pageerror',error=>errors.push(error.message));page.on('console',message=>{if(message.type()==='error')errors.push(message.text());});page.on('dialog',dialog=>dialog.accept());
      await page.goto(origin+'/#router');await page.locator('#usage-chart svg').waitFor();await page.evaluate(()=>clearInterval(pollTimer));
      await noOverflow(page,`${width}/${scheme} router`);
      const file=path.join(screenshots,`${width}-${scheme}-router.png`);await page.evaluate(()=>{document.activeElement?.blur();scrollTo(0,0);});await page.waitForTimeout(100);await page.screenshot({path:file,fullPage:true});console.log(`Screenshot: ${file}`);
      await page.locator('#chart-range').selectOption('15');await page.locator('#usage-chart svg').waitFor();
      assert((await page.locator('#usage-chart svg').getAttribute('aria-label')).includes('15 минут'));
      await page.locator('#usage-chart svg').focus();await page.keyboard.press('Home');assert((await page.locator('#usage-chart .chart-tooltip').textContent()).includes('CPU:'));
      await page.keyboard.press('Escape');
      await navigate(page,'nodes',width);await page.locator('#nodes-body tr').first().waitFor();
      await page.locator('#node-sort').selectOption('latency');assert((await page.locator('#nodes-body tr').first().textContent()).includes('Zero latency'));
      await page.locator('#node-source-filter').selectOption('Backup');assert.equal(await page.locator('#nodes-body tr').count(),2);
      await page.locator('#node-status-filter').selectOption('unchecked');assert((await page.locator('#nodes-body').textContent()).includes('Unchecked node'));
      await page.locator('#node-status-filter').selectOption('');await page.locator('#node-source-filter').selectOption('');
      if(width<761){await page.locator('.node-details summary').first().click();assert(await page.locator('.node-details').first().getAttribute('open')!==null);}
      await noOverflow(page,`${width}/${scheme} nodes`);
      await navigate(page,'subscriptions',width);await page.locator('#add-subscription').click();await page.locator('#subscription-modal').waitFor({state:'visible'});
      assert.equal(await page.evaluate(()=>document.activeElement.id),'subscription-id');assert.equal(await page.locator('main').evaluate(el=>el.inert),true);
      await page.locator('#subscription-modal button[type="submit"]').focus();await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>document.activeElement.id),'subscription-id');
      await page.keyboard.press('Escape');assert(await page.locator('#subscription-modal').evaluate(el=>el.classList.contains('hidden')));assert.equal(await page.evaluate(()=>document.activeElement.id),'add-subscription');
      const editSource=page.locator('.subscription-edit[data-id="primary"]');await editSource.focus();await editSource.click();
      await page.locator('#subscription-name').fill('Primary renamed');await page.locator('#subscription-form button[type="submit"]').click();
      await page.waitForFunction(()=>document.querySelector('#subscriptions-body').textContent.includes('Primary renamed'),{},{timeout:1000});
      assert.equal(await page.evaluate(()=>document.activeElement.dataset.id),'primary','subscription save lost keyboard row focus');
      const sourceButton=page.locator('.subscription-edit[data-id="primary"]');await sourceButton.evaluate(el=>window.fixtureSourceElement=el);await page.evaluate(()=>loadSubscriptions());
      assert(await sourceButton.evaluate(el=>el===window.fixtureSourceElement),'background subscription refresh replaced focused control');
      state.sources[0].name='Background source change';await page.evaluate(()=>loadSubscriptions());assert((await page.locator('#subscriptions-body').textContent()).includes('Background source change'),'late source result left stale label');assert.equal(await page.evaluate(()=>document.activeElement.dataset.id),'primary','late source result lost keyboard row focus');
      await page.locator('.subscription-delete[data-id="primary"]').focus();await page.locator('.subscription-delete[data-id="primary"]').click();
      await page.waitForFunction(()=>!document.querySelector('.subscription-delete[data-id="primary"]'),{},{timeout:1000});assert.equal(await page.evaluate(()=>document.activeElement.id),'add-subscription','deleted subscription focus fallback');
      await navigate(page,'users',width);const editUser=page.locator('.user-edit[data-id="fixture-user"]');await editUser.focus();await editUser.click();
      await page.locator('#user-name').fill('operator renamed');await page.locator('#user-form button[type="submit"]').click();
      await page.waitForFunction(()=>document.querySelector('#users-body').textContent.includes('operator renamed'),{},{timeout:1000});
      assert.equal(await page.evaluate(()=>document.activeElement.dataset.id),'fixture-user','user save lost keyboard row focus');
      const toggleUserButton=page.locator('.user-toggle[data-id="fixture-user"]');await toggleUserButton.focus();await toggleUserButton.click();
      await page.waitForFunction(()=>document.querySelector('.user-toggle[data-id="fixture-user"]').textContent==='Включить',{},{timeout:1000});
      assert.equal(await page.evaluate(()=>document.activeElement.dataset.id),'fixture-user','user toggle lost keyboard row focus');
      const freshToggle=page.locator('.user-toggle[data-id="fixture-user"]');await freshToggle.evaluate(el=>window.fixtureUserElement=el);await page.evaluate(()=>loadUsers());
      assert(await freshToggle.evaluate(el=>el===window.fixtureUserElement),'unchanged user refresh replaced focused control');state.users.find(user=>user.id==='fixture-user').username='Background user change';await page.evaluate(()=>loadUsers());assert((await page.locator('#users-body').textContent()).includes('Background user change'),'late user result left stale label');assert.equal(await page.evaluate(()=>document.activeElement.dataset.id),'fixture-user','late user result lost keyboard row focus');
      await page.locator('.user-delete[data-id="fixture-user"]').focus();await page.locator('.user-delete[data-id="fixture-user"]').click();
      await page.waitForFunction(()=>!document.querySelector('.user-delete[data-id="fixture-user"]'),{},{timeout:1000});assert.equal(await page.evaluate(()=>document.activeElement.id),'add-user','deleted user focus fallback');
      await navigate(page,'nodes',width);await page.locator('.slot-switch[data-index="1"]').focus();await page.locator('.slot-switch[data-index="1"]').click();
      await page.waitForFunction(()=>document.querySelector('.pool-item.active .slot-switch')?.dataset.index==='1',{},{timeout:1500});
      assert.equal(await page.evaluate(()=>document.activeElement.dataset.index),'1','pool switch lost keyboard slot focus');state.activeSlot=0;await page.evaluate(()=>loadStatus());assert.equal(await page.locator('.pool-item.active .slot-switch').getAttribute('data-index'),'0','late accepted switch status stayed stale');assert.equal(await page.evaluate(()=>document.activeElement.dataset.index),'1','late switch result lost focused button key');
      await navigate(page,'devices',width);await page.locator('.policy').waitFor();await page.locator('.policy').focus();await page.locator('.policy').evaluate(el=>{window.fixturePolicyElement=el;el.value='xkeen';});state.policyVersion++;
      await page.evaluate(()=>poll());assert(await page.locator('.policy').evaluate(el=>el===window.fixturePolicyElement),'poll replaced focused policy select');assert.equal(await page.locator('.policy').inputValue(),'xkeen','metadata poll changed native selection');assert(await page.locator('.policy').evaluate(el=>document.activeElement===el),'metadata poll lost native select focus');assert((await page.locator('#clients-body tr td').nth(0).textContent()).includes('Workstation 1'),'focused select froze client name metadata');assert.equal(await page.locator('#clients-body tr td').nth(3).textContent(),'Default route','focused select froze committed policy metadata');
      state.permissions=permissions.filter(p=>p!=='router.policy');await page.evaluate(()=>poll());assert.equal(await page.locator('.policy').count(),0,'revocation left focused policy control');
      state.permissions=[...permissions];await page.evaluate(()=>poll());
      await navigate(page,'settings',width);await page.locator('#setting-benchmark-full_interval').waitFor();
      assert.equal(await page.locator('#settings-fields input').count(),36);assert.equal(await page.locator('#setting-benchmark-full_interval').inputValue(),'5m');assert.equal(await page.locator('#setting-subscriptions-cache_enabled').isChecked(),false);
      const details=page.locator('details.settings-advanced');
      for(let i=0;i<await details.count();i++){await details.nth(i).locator('summary').click();assert(await details.nth(i).getAttribute('open')!==null);}
      assert(await page.locator('#setting-benchmark-speed-url_template').isHidden(),'disabled speed details visible');
      await page.locator('#setting-benchmark-full_interval').fill('7m');await page.locator('#settings-save').click();await page.waitForFunction(()=>document.querySelector('#settings-save').disabled&&!settingsSaving);assert.equal(state.settings.benchmark.full_interval,'7m');assert.equal(state.settings.benchmark.speed.url_template,'https://speed.example.invalid/?bytes={bytes}','hidden values lost in save');
      await noOverflow(page,`${width}/${scheme} settings`);
      await page.evaluate(()=>scrollTo(0,300));assert(Math.abs(await page.locator('header').evaluate(el=>el.getBoundingClientRect().top))<1,'workspace header does not stay at top on scroll');await page.evaluate(()=>{document.activeElement?.blur();scrollTo(0,0);});await page.waitForTimeout(100);await page.screenshot({path:path.join(screenshots,`${width}-${scheme}-settings.png`),fullPage:true});
      state.panelStatus='awaiting_confirmation';await page.evaluate(()=>loadPanelStatus(true));
      assert.equal(await page.locator('#panel-step-confirm').getAttribute('aria-current'),'step');
      assert((await page.locator('#panel-countdown').textContent()).includes('До автоматического возврата'));
      assert(!state.requests.some(req=>req.route==='/api/v1/panel/confirm'),'trial countdown automatically confirmed');
      state.panelStatus='idle';await page.evaluate(async()=>{await pollPanelTrial(panelGeneration);await loadPanelStatus(true);});await page.waitForFunction(()=>!pollLoading);
      await page.evaluate(()=>{clearInterval(pollTimer);window.fixtureHidden=true;Object.defineProperty(document,'hidden',{configurable:true,get:()=>window.fixtureHidden});});
      const beforeHidden=state.requests.length;await page.evaluate(()=>poll());assert.equal(state.requests.length,beforeHidden,'hidden page still polls ordinary API');
      await page.evaluate(()=>{window.fixtureHidden=false;document.dispatchEvent(new Event('visibilitychange'));});await page.waitForFunction(()=>!pollLoading);
      assert(state.requests.slice(beforeHidden).some(req=>req.route==='/api/v1/session'),'resume did not refresh authentication');
      await page.evaluate(()=>showLogin());await page.locator('#username').waitFor();await page.keyboard.press('Escape');assert(await page.locator('#login').isVisible(),'required login dismissed by Escape');assert.equal(await page.locator('main').evaluate(el=>el.inert),true);
      assert.deepEqual(errors,[],`${width}/${scheme} browser errors`);
      await context.close();
    }
    console.log('Real browser desktop/mobile light/dark navigation, focus, charts, filters, settings and overflow checks passed.');
  } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
}
main().catch(error=>{console.error(error);server.close();process.exitCode=1;});
