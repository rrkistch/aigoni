// Real Linux container lifecycle; only this script's named container and volume are removed.
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),crypto=require('node:crypto');
const exec=require('node:util').promisify(require('node:child_process').execFile);
const root=path.resolve(__dirname,'../..');
const archive=path.resolve(process.env.AIGONI_THEME_PACKAGE||path.join(root,'.firefly/theme-artifacts/aigoni-theme-v0.1.2-linux-amd64.tar.gz'));
const image=process.env.AIGONI_TEST_IMAGE||'aigoni-firefly:theme-smoke';
const migrationImage=process.env.AIGONI_MIGRATION_IMAGE||image;
const updateArchive=process.env.AIGONI_THEME_UPDATE_PACKAGE&&path.resolve(process.env.AIGONI_THEME_UPDATE_PACKAGE);
const checkMCP=process.env.AIGONI_MCP_SMOKE==='1';
(async()=>{
 const info=await fs.stat(archive),tag=path.basename(archive).match(/^aigoni-(theme-v\d+\.\d+\.\d+)-linux-amd64\.tar\.gz$/)?.[1];assert.ok(tag);
 const name='aigoni-theme-smoke-'+crypto.randomBytes(6).toString('hex'),volume=name+'-data',password='Fixture$#'+crypto.randomBytes(24).toString('hex');
 const docker=async(...args)=>(await exec('docker',args,{maxBuffer:3000000,timeout:120000})).stdout.trim();
 let volumeCreated=false,containerCreated=false,origin,cookie='',csrf='';
 async function request(p,method='GET',body,raw=false,auth=true){const headers=auth?{Cookie:cookie,'X-CSRF-Token':csrf}:{};if(body!==undefined&&!(body instanceof FormData))headers['Content-Type']=raw?'application/octet-stream':'application/json';return fetch(origin+p,{method,headers,body:body===undefined?undefined:(raw||body instanceof FormData)?body:JSON.stringify(body),signal:AbortSignal.timeout(30000)});}
 async function json(p,method='GET',body){const response=await request(p,method,body);let data;try{data=await response.json();}catch{throw new Error(`${p}: HTTP ${response.status}`)};assert.ok(response.ok,`${p}: ${JSON.stringify(data)}`);return data;}
 async function until(fn,label,ms=900000){const end=Date.now()+ms;while(Date.now()<end){const value=await fn();if(value)return value;await new Promise(r=>setTimeout(r,1500));}throw new Error('Timed out: '+label);}
 async function start(startImage=image,maintenance=false){await docker('run','-d','--name',name,'--init','--memory=2g','--cpus=1','--cap-drop=ALL','--security-opt=no-new-privileges:true','-p','127.0.0.1::8080','-v',volume+':/data','-e','ADMIN_PASSWORD='+password,'-e','ADMIN_PATH=studio','-e','FRONTEND_MODE=firefly','-e','COOKIE_SECURE=false','-e','AIGONI_UPLOAD_ALLOWED_EXTS=jpg,jpeg,png,webp,gif','-e','FIREFLY_REBUILD_ON_START=false',startImage);containerCreated=true;origin='http://'+await docker('port',name,'8080/tcp');await until(async()=>{try{return(await request(maintenance?'/studio/maintenance':'/rest/v1','GET',undefined,false,false)).ok;}catch{return false;}},'startup',60000);const response=await request('/api/admin/v1/session','POST',{password},false,false);assert.equal(response.status,200);cookie=response.headers.get('set-cookie').split(';')[0];csrf=(await response.json()).csrf_token;}
 async function published(after=0,theme){return until(async()=>{const state=await json('/api/admin/v1/site-build');if(state.status==='failed')throw new Error('Build failed: '+state.error+'\n'+await docker('exec',name,'tail','-n','50','/data/.firefly/build.log'));return state.status==='published'&&state.published_revision>after&&(!theme||state.theme===theme)?state:null;},'publishing');}
 async function upload(endpoint,file,size,extra={}){let state=await json(endpoint,'POST',{name:path.basename(file),size,...extra});const input=await fs.open(file,'r');try{while(state.offset<size){const buffer=Buffer.alloc(Math.min(8*1024**2,size-state.offset));const {bytesRead}=await input.read(buffer,0,buffer.length,state.offset);assert.ok(bytesRead);const response=await request(endpoint+'/'+state.id+'?offset='+state.offset,'PUT',buffer.subarray(0,bytesRead),true);assert.equal(response.status,200,await response.clone().text());state=await response.json();}}finally{await input.close()};await json(endpoint+'/'+state.id+'/complete','POST');return state.id;}
 async function backupState(id,wanted){return until(async()=>{const state=await json('/api/admin/v1/backups/items/'+id);if(state.status==='failed')throw new Error(state.error);return state.status===wanted?state:null;},'backup '+wanted);}
 try{
  await docker('volume','create',volume);volumeCreated=true;await start(migrationImage);let state=await published();
  assert.equal((await request('/studio/maintenance','GET',undefined,false,false)).status,200);
  assert.equal((await request('/api/admin/v1/theme','GET',undefined,false,false)).status,401);
  const post=await json('/api/admin/v1/posts','POST',{title:'Theme migration smoke',slug:'theme-migration-smoke',date:new Date().toISOString(),publish:true,body:'CURRENT_THEME_SMOKE_CONTENT'});
  state=await published(state.published_revision);console.log('PASS existing integrated data volume');
  assert.equal(await docker('exec',name,'id','-u'),'1000');
  const note=await json('/api/admin/v1/notes','POST',{title:'Private migration note',date:new Date().toISOString(),body:'PRIVATE_MIGRATION_SENTINEL'});const noteID=note.id.split('/').map(encodeURIComponent).join('/');
  const form=new FormData();form.append('asset',new Blob([Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII=','base64')],{type:'image/png'}),'private.png');const attachment=await request('/api/admin/v1/notes/'+noteID+'/assets','POST',form);assert.equal(attachment.status,201,await attachment.clone().text());const privateAsset=await attachment.json();assert.equal((await request(privateAsset.path,'GET',undefined,false,false)).status,404);

  if(migrationImage!==image){await docker('stop','--time','30',name);await docker('rm',name);containerCreated=false;await start(image);assert.ok((await(await request('/posts/theme-migration-smoke/','GET',undefined,false,false)).text()).includes('CURRENT_THEME_SMOKE_CONTENT'));assert.equal(await docker('exec',name,'node','-e',"console.log(require('fs').existsSync('/opt/firefly/scripts/aigoni-build.mjs'))"),'false');console.log('PASS backend-only image migration retains last public release');}

  await upload('/api/admin/v1/theme/uploads',archive,info.size,{tag});
  await until(async()=>{const data=await json('/api/admin/v1/theme');if(['failed','cancelled'].includes(data.upload?.status))throw new Error(data.upload.error);return data.upload?.status==='ready';},'trusted package verification');
  await json('/api/admin/v1/theme/activate','POST',{version:tag});state=await published(state.published_revision,tag);
  assert.ok((await(await request('/posts/theme-migration-smoke/','GET',undefined,false,false)).text()).includes('CURRENT_THEME_SMOKE_CONTENT'));
  assert.equal(await docker('exec',name,'node','-e',"console.log(require('fs').existsSync('/data/.aigoni-admin/themes/versions/"+tag+"/source/.aigoni'))"),'false');console.log('PASS verified package install, immutable source, atomic activation');
  let rollbackTarget='source';
  if(updateArchive){const nextInfo=await fs.stat(updateArchive),nextTag=path.basename(updateArchive).match(/^aigoni-(theme-v\d+\.\d+\.\d+)-linux-amd64\.tar\.gz$/)?.[1];assert.ok(nextTag);await upload('/api/admin/v1/theme/uploads',updateArchive,nextInfo.size,{tag:nextTag});await until(async()=>{const data=await json('/api/admin/v1/theme');if(data.upload?.status==='failed')throw new Error(data.upload.error);return data.upload?.status==='ready';},'theme upgrade verification');await json('/api/admin/v1/theme/activate','POST',{version:nextTag});state=await published(state.published_revision,nextTag);rollbackTarget=tag;console.log('PASS independent theme upgrade without replacing backend image');}

  const settings=await json('/api/admin/v1/theme-settings');await json('/api/admin/v1/theme-settings','PATCH',{revision:settings.revision,groups:{appearance:{themeColor:{hue:225}}}});
  const current=await json('/api/admin/v1/posts/'+post.id);
  if(checkMCP){
   const credentials=await json('/api/admin/v1/integrations');const issued=await json('/api/admin/v1/integrations','POST',{revision:credentials.revision,name:'Hermes container smoke',endpoint:origin+'/mcp'});
   let sequence=0;async function rpc(method,params){const response=await fetch(origin+'/mcp',{method:'POST',headers:{Authorization:'Bearer '+issued.token,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-11-25'},body:JSON.stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(30000)});assert.equal(response.status,200);const envelope=await response.json();assert.ok(!envelope.error,JSON.stringify(envelope.error));return envelope.result;}
   await rpc('initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'hermes-deployment-check',version:'1'}});
   const tools=await rpc('tools/list',{});assert.ok(!tools.tools.some(tool=>tool.name.startsWith('wiki_')||tool.name==='note_get'));
   const result=await rpc('tools/call',{name:'public_change_propose',arguments:{request_id:'runtime-public-change',type:'post',id:current.id,revision:current.revision,action:'publish',fields:{body:'LATEST_CONTENT_ON_THEME_ROLLBACK'}}});assert.ok(!result.isError);const candidate=JSON.parse(result.content[0].text);assert.equal(candidate.status,'pending');assert.equal((await json('/api/admin/v1/posts/'+post.id)).body,current.body);
   await json('/api/admin/v1/approvals/'+candidate.id+'/approve','POST',{revision:candidate.revision});console.log('PASS actual MCP HTTP candidate requires human approval, no model invocation');
  }else await json('/api/admin/v1/posts/'+post.id,'PATCH',{revision:current.revision,body:'LATEST_CONTENT_ON_THEME_ROLLBACK'});
  await json('/api/admin/v1/theme/rollback','POST',{});state=await published(state.published_revision,rollbackTarget);
  assert.ok((await(await request('/posts/theme-migration-smoke/','GET',undefined,false,false)).text()).includes('LATEST_CONTENT_ON_THEME_ROLLBACK'));
  assert.equal((await json('/api/admin/v1/theme-settings')).groups.appearance.themeColor.hue,225);console.log('PASS settings + article changes and rollback rebuild current data');
  const created=await json('/api/admin/v1/backups','POST',{});await backupState(created.id,'ready');
  const download=await request('/api/admin/v1/backups/items/'+created.id+'/download');assert.equal(download.status,200);const backup=Buffer.from(await download.arrayBuffer());
  const uploaded=await json('/api/admin/v1/backups/uploads','POST',{name:'restore.tar.gz',size:backup.length});let offset=0;while(offset<backup.length){const end=Math.min(offset+8*1024**2,backup.length);const response=await request('/api/admin/v1/backups/uploads/'+uploaded.id+'?offset='+offset,'PUT',backup.subarray(offset,end),true);assert.equal(response.status,200);offset=(await response.json()).offset;}
  await json('/api/admin/v1/backups/uploads/'+uploaded.id+'/complete','POST');await backupState(uploaded.id,'validated');await json('/api/admin/v1/backups/items/'+uploaded.id+'/restore','POST',{confirm:true});const restored=await backupState(uploaded.id,'restored');assert.ok(restored.safety_backup);state=await published(state.published_revision);console.log('PASS container backup validation and restore + safety backup');
  await docker('stop','--time','30',name);await docker('rm',name);containerCreated=false;await start();
  assert.equal((await json('/api/admin/v1/theme-settings')).groups.appearance.themeColor.hue,225);assert.ok((await json('/api/admin/v1/theme')).previous);
  assert.equal((await json('/api/admin/v1/notes/'+noteID)).body,'PRIVATE_MIGRATION_SENTINEL');assert.equal((await request(privateAsset.path,'GET',undefined,false,false)).status,404);assert.equal((await request(privateAsset.path)).status,200);
  await docker('exec',name,'node','/usr/local/lib/aigoni/healthcheck.cjs');console.log('PASS 1 CPU / 2 GiB migration, persistence, package activation, rollback and restore');
  if(checkMCP){
   const before=await json('/api/admin/v1/site-build');await docker('exec',name,'node','-e',"require('fs').writeFileSync('/data/.aigoni-admin/restore-journal.json','{}')");await docker('stop','--time','30',name);await docker('rm',name);containerCreated=false;await start(image,true);
   assert.equal((await request('/','GET',undefined,false,false)).status,503);assert.equal((await request('/studio/maintenance','GET',undefined,false,false)).status,200);
   await docker('exec',name,'node','-e',"require('fs').unlinkSync('/data/.aigoni-admin/restore-journal.json')");await json('/api/admin/v1/backups/recover','POST',{confirm:true});await published(before.published_revision);console.log('PASS failed startup recovery keeps authenticated maintenance available and resumes safely');
  }
 }catch(error){if(containerCreated){try{console.error(await docker('logs','--tail','40',name));}catch{}}const reason=String(error.message||error).replaceAll(password,'[fixture redacted]').replace(/aigoni_mcp_[A-Za-z0-9_.-]+/g,'[fixture credential redacted]');if(process.env.GITHUB_ACTIONS)console.error('::error title=Deployment acceptance::'+reason.slice(0,6000).replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A'));throw new Error(reason);}finally{
  if(containerCreated)await docker('rm','-f',name);if(volumeCreated)await docker('volume','rm',volume);
 }
})().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
