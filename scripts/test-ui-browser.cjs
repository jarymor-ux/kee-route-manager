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
function settingsFixture() {
  return {
    benchmark: { full_interval:'5m',batch_size:20,latency_workers:8,requests_per_weight:2,finalists:6,min_improvement_percent:15,switch_cooldown:'10m',stability_before_upgrade:'10m',speed:{enabled:false,workers:2,url_template:'https://speed.example.invalid/?bytes={bytes}',warmup_bytes:'8 MiB',min_sample_bytes:'64 MiB',max_sample_bytes:'512 MiB',target_duration:'6s',repetitions:3} },
    health:{recovery_threshold:2,request_timeout:'8s',max_response_bytes:'64 KiB',hot_pool_freshness:'5m',provider_retry_backoff:['15s','30s','1m','2m','5m','10m']},
    failover:{detection_interval:'5s',failure_threshold:2,probe_timeout:'2s',overall_deadline:'5s',quorum:2},
    subscriptions:{cache_enabled:false,cache_ttl:'168h',refresh_interval:'30m',request_timeout:'20s',max_response_bytes:'4 MiB',max_nodes_per_source:500,max_sources:20,max_nodes:500},
    pool:{provider_diversity:{enabled:false,max_per_provider:1}},
  };
}
const state = { permissions:[...permissions], logged:true, revision:'fixture-original', settings:settingsFixture(), policyVersion:0, requests:[], saves:[], panelStatus:'idle' };
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
  if(route === '/api/v1/status') return {version:'1.3.0-fixture',xray_running:true,capabilities:{client_policy:true,wake_on_lan:true},state:{xray_configured:true,automatic_routing_paused:false,direct_mode:false,active_slot:0,active_node_id:'zero',health_status:'healthy',pool:[{index:0,node_id:'zero',label:'Zero latency',score:0}],sources:{Primary:{name:'Primary',status:'healthy',node_count:3}},last_benchmark:{finished_at:now()}}};
  if(route === '/api/v1/nodes') return nodes;
  if(route === '/api/v1/router/clients') return {updated_at:now(),value:[{name:`Workstation ${state.policyVersion}`,mac:'02:00:00:00:00:01',ip:'192.168.1.2',active:true,connection_policy:'XKeen',link:'LAN'}]};
  if(route === '/api/v1/router/metrics') return metrics();
  if(route === '/api/v1/router/metrics/history') return {samples:[1200000,180000,175000,50000,45000].map((age,i)=>({...metrics(),cpu_percent:i*10,ram_percent:40,updated_at:new Date(Date.now()-age).toISOString()}))};
  if(route === '/api/v1/subscriptions') return [{id:'primary',name:'Primary',url:'https://subscription.example.invalid/private-fixture',enabled:true,headers:{Authorization:'Bearer synthetic-fixture'}}];
  if(route === '/api/v1/users') return {users:[{id:'fixture-admin',username:'admin',enabled:true,permissions}],permissions,roles:{admin:permissions,viewer:['vpn.view','router.view']}};
  if(route === '/api/v1/events') return [];
  if(route === '/api/v1/update/status') return {enabled:true,launcher:true,channel:'stable',channel_switch_supported:true,phase:'idle',current_version:'1.3.0-fixture'};
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
      state.logged=true;state.permissions=[...permissions];state.settings=settingsFixture();state.revision='fixture-original';state.policyVersion=0;state.panelStatus='idle';
      const context=await browser.newContext({viewport:{width,height:width<761?844:1000},colorScheme:scheme,serviceWorkers:'block'});
      const page=await context.newPage(); const errors=[];
      page.on('pageerror',error=>errors.push(error.message));page.on('console',message=>{if(message.type()==='error')errors.push(message.text());});page.on('dialog',dialog=>dialog.accept());
      await page.goto(origin+'/#router');await page.locator('#usage-chart svg').waitFor();
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
      await navigate(page,'devices',width);await page.locator('.policy').waitFor();await page.locator('.policy').focus();await page.locator('.policy').evaluate(el=>{window.fixturePolicyElement=el;});state.policyVersion++;
      await page.evaluate(()=>poll());assert(await page.locator('.policy').evaluate(el=>el===window.fixturePolicyElement),'poll replaced focused policy select');
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
