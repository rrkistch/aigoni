// Real Linux container lifecycle; only this script's named container and volume are removed.
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),crypto=require('node:crypto');
const exec=require('node:util').promisify(require('node:child_process').execFile);
const root=path.resolve(__dirname,'../..');
const archive=path.resolve(process.env.AIGONI_THEME_PACKAGE||path.join(root,'.firefly/theme-artifacts/aigoni-theme-v0.1.2-linux-amd64.tar.gz'));
const image=process.env.AIGONI_TEST_IMAGE||'aigoni-firefly:theme-smoke';
(async()=>{
 const info=await fs.stat(archive),tag=path.basename(archive).match(/^aigoni-(theme-v\d+\.\d+\.\d+)-linux-amd64\.tar\.gz$/)?.[1];assert.ok(tag);
 const name='aigoni-theme-smoke-'+crypto.randomBytes(6).toString('hex'),volume=name+'-data',password='Fixture$#'+crypto.randomBytes(24).toString('hex');
 const docker=async(...args)=>(await exec('docker',args,{maxBuffer:3000000,timeout:120000})).stdout.trim();
 let volumeCreated=false,containerCreated=false,origin,cookie='',csrf='';
 async function request(p,method='GET',body,raw=false,auth=true){const headers=auth?{Cookie:cookie,'X-CSRF-Token':csrf}:{};if(body!==undefined)headers['Content-Type']=raw?'application/octet-stream':'application/json';return fetch(origin+p,{method,headers,body:body===undefined?undefined:raw?body:JSON.stringify(body),signal:AbortSignal.timeout(30000)});}
 async function json(p,method='GET',body){const response=await request(p,method,body);let data;try{data=await response.json();}catch{throw new Error(`${p}: HTTP ${response.status}`)};assert.ok(response.ok,`${p}: ${JSON.stringify(data)}`);return data;}
 async function until(fn,label,ms=900000){const end=Date.now()+ms;while(Date.now()<end){const value=await fn();if(value)return value;await new Promise(r=>setTimeout(r,1500));}throw new Error('Timed out: '+label);}
 async function start(){await docker('run','-d','--name',name,'--init','--memory=2g','--cpus=1','--cap-drop=ALL','--security-opt=no-new-privileges:true','-p','127.0.0.1::8080','-v',volume+':/data','-e','ADMIN_PASSWORD='+password,'-e','ADMIN_PATH=studio','-e','FRONTEND_MODE=firefly','-e','COOKIE_SECURE=false','-e','FIREFLY_REBUILD_ON_START=false',image);containerCreated=true;origin='http://'+await docker('port',name,'8080/tcp');await until(async()=>{try{return(await request('/rest/v1','GET',undefined,false,false)).ok;}catch{return false;}},'startup',60000);const response=await request('/api/admin/v1/session','POST',{password},false,false);assert.equal(response.status,200);cookie=response.headers.get('set-cookie').split(';')[0];csrf=(await response.json()).csrf_token;}
 async function published(after=0,theme){return until(async()=>{const state=await json('/api/admin/v1/site-build');if(state.status==='failed')throw new Error('Build failed: '+state.error+'\n'+await docker('exec',name,'tail','-n','50','/data/.firefly/build.log'));return state.status==='published'&&state.published_revision>after&&(!theme||state.theme===theme)?state:null;},'publishing');}
 async function upload(endpoint,file,size,extra={}){let state=await json(endpoint,'POST',{name:path.basename(file),size,...extra});const input=await fs.open(file,'r');try{while(state.offset<size){const buffer=Buffer.alloc(Math.min(8*1024**2,size-state.offset));const {bytesRead}=await input.read(buffer,0,buffer.length,state.offset);assert.ok(bytesRead);const response=await request(endpoint+'/'+state.id+'?offset='+state.offset,'PUT',buffer.subarray(0,bytesRead),true);assert.equal(response.status,200,await response.clone().text());state=await response.json();}}finally{await input.close()};await json(endpoint+'/'+state.id+'/complete','POST');return state.id;}
 async function backupState(id,wanted){return until(async()=>{const state=await json('/api/admin/v1/backups/items/'+id);if(state.status==='failed')throw new Error(state.error);return state.status===wanted?state:null;},'backup '+wanted);}
 try{
  await docker('volume','create',volume);volumeCreated=true;await start();let state=await published();
  assert.equal((await request('/studio/maintenance','GET',undefined,false,false)).status,200);
  assert.equal((await request('/api/admin/v1/theme','GET',undefined,false,false)).status,401);
  const post=await json('/api/admin/v1/posts','POST',{title:'Theme migration smoke',slug:'theme-migration-smoke',date:new Date().toISOString(),publish:true,body:'CURRENT_THEME_SMOKE_CONTENT'});
  state=await published(state.published_revision);console.log('PASS existing integrated data volume');
  await upload('/api/admin/v1/theme/uploads',archive,info.size,{tag});
  await until(async()=>{const data=await json('/api/admin/v1/theme');if(['failed','cancelled'].includes(data.upload?.status))throw new Error(data.upload.error);return data.upload?.status==='ready';},'trusted package verification');
  await json('/api/admin/v1/theme/activate','POST',{version:tag});state=await published(state.published_revision,tag);
  assert.ok((await(await request('/posts/theme-migration-smoke/','GET',undefined,false,false)).text()).includes('CURRENT_THEME_SMOKE_CONTENT'));
  assert.equal(await docker('exec',name,'node','-e',"console.log(require('fs').existsSync('/data/.aigoni-admin/themes/versions/"+tag+"/source/.aigoni'))"),'false');console.log('PASS verified package install, immutable source, atomic activation');
  const settings=await json('/api/admin/v1/theme-settings');await json('/api/admin/v1/theme-settings','PATCH',{revision:settings.revision,groups:{appearance:{themeColor:{hue:225}}}});
  const current=await json('/api/admin/v1/posts/'+post.id);await json('/api/admin/v1/posts/'+post.id,'PATCH',{revision:current.revision,body:'LATEST_CONTENT_ON_THEME_ROLLBACK'});
  await json('/api/admin/v1/theme/rollback','POST',{});state=await published(state.published_revision,'source');
  assert.ok((await(await request('/posts/theme-migration-smoke/','GET',undefined,false,false)).text()).includes('LATEST_CONTENT_ON_THEME_ROLLBACK'));
  assert.equal((await json('/api/admin/v1/theme-settings')).groups.appearance.themeColor.hue,225);console.log('PASS settings + article changes and rollback rebuild current data');
  const created=await json('/api/admin/v1/backups','POST',{});await backupState(created.id,'ready');
  const download=await request('/api/admin/v1/backups/items/'+created.id+'/download');assert.equal(download.status,200);const backup=Buffer.from(await download.arrayBuffer());
  const uploaded=await json('/api/admin/v1/backups/uploads','POST',{name:'restore.tar.gz',size:backup.length});let offset=0;while(offset<backup.length){const end=Math.min(offset+8*1024**2,backup.length);const response=await request('/api/admin/v1/backups/uploads/'+uploaded.id+'?offset='+offset,'PUT',backup.subarray(offset,end),true);assert.equal(response.status,200);offset=(await response.json()).offset;}
  await json('/api/admin/v1/backups/uploads/'+uploaded.id+'/complete','POST');await backupState(uploaded.id,'validated');await json('/api/admin/v1/backups/items/'+uploaded.id+'/restore','POST',{confirm:true});const restored=await backupState(uploaded.id,'restored');assert.ok(restored.safety_backup);state=await published(state.published_revision);console.log('PASS container backup validation and restore + safety backup');
  await docker('stop','--time','30',name);await docker('rm',name);containerCreated=false;await start();
  assert.equal((await json('/api/admin/v1/theme-settings')).groups.appearance.themeColor.hue,225);assert.equal((await json('/api/admin/v1/theme')).previous,tag);
  await docker('exec',name,'node','/usr/local/lib/aigoni/healthcheck.cjs');console.log('PASS 1 CPU / 2 GiB migration, persistence, package activation, rollback and restore');
 }catch(error){if(containerCreated){try{console.error(await docker('logs','--tail','40',name));}catch{}}throw error;}finally{
  if(containerCreated)await docker('rm','-f',name);if(volumeCreated)await docker('volume','rm',volume);
 }
})().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
