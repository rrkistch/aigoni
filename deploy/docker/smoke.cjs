// Run after: docker build -t aigoni-firefly:docker-smoke .
const assert=require("node:assert/strict"),fs=require("node:fs/promises");
const path=require("node:path"),os=require("node:os"),crypto=require("node:crypto");
const exec=require("node:util").promisify(require("node:child_process").execFile);
const root=path.resolve(__dirname,"../..");
(async()=>{
 const project="aigoni-smoke-"+crypto.randomBytes(6).toString("hex");
 const temp=await fs.mkdtemp(path.join(os.tmpdir(),"aigoni-docker-"));
 const file=path.join(temp,"docker.env"),password="S$#"+crypto.randomBytes(24).toString("hex");
 await fs.writeFile(file,["ADMIN_PASSWORD="+password,"ADMIN_PATH=studio","COOKIE_SECURE=false","FRONTEND_MODE=firefly","FIREFLY_REBUILD_ON_START=true","AIGONI_UPLOAD_ALLOWED_EXTS=jpg,jpeg,png,webp,gif"].join("\n"),{mode:0o600});
 const env={...process.env,AIGONI_ENV_FILE:file,AIGONI_IMAGE:process.env.AIGONI_TEST_IMAGE||"aigoni-firefly:docker-smoke",AIGONI_PORT:"0",AIGONI_BIND_ADDRESS:"127.0.0.1"};
 const prefix=["compose","-p",project,"--env-file",path.join(root,"compose.env.example"),"-f",path.join(root,"compose.yaml"),"-f",path.join(root,"deploy/docker/compose.limited.yaml")];
 const docker=async(...args)=>(await exec("docker",[...prefix,...args],{cwd:root,env,maxBuffer:4000000,timeout:120000})).stdout.trim();
 let created=false,origin,cookie="",csrf="";
 async function request(p,method="GET",body,auth=true){
  const headers=auth?{Cookie:cookie,"X-CSRF-Token":csrf}:{};
  if(body!==undefined&&!(body instanceof FormData))headers["Content-Type"]="application/json";
  return fetch(origin+p,{method,headers,signal:AbortSignal.timeout(10000),body:body===undefined?undefined:body instanceof FormData?body:JSON.stringify(body)});
 }
 async function until(fn,label,ms=600000){
  const end=Date.now()+ms;
  while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,1000));}
  throw new Error("Timed out: "+label);
 }
 async function login(){
  origin="http://"+await docker("port","aigoni","8080");
  await until(async()=>{try{return(await request("/rest/v1","GET",undefined,false)).ok;}catch{return false;}},"startup",60000);
  const response=await request("/api/admin/v1/session","POST",{password},false);
  assert.equal(response.status,200,"literal $/# password must work");
  cookie=response.headers.get("set-cookie").split(";")[0];csrf=(await response.json()).csrf_token;
 }
 async function published(after){
  let state;
  await until(async()=>{
   state=await(await request("/api/admin/v1/site-build")).json();
   if(state.status==="failed")throw new Error("Build failed:\n"+await docker("exec","-T","aigoni","tail","-n","100","/data/.firefly/build.log"));
   return state.status==="published"&&state.published_revision>after;
  },"publish");
  return state;
 }
 try{
  await docker("config","--quiet");created=true;
  await docker("up","-d","--no-build","--pull","never");
  await login();
  const packageFile=process.env.AIGONI_THEME_PACKAGE;if(!packageFile)throw new Error("AIGONI_THEME_PACKAGE must point to the published official theme package");
  const packageName=path.basename(packageFile),tag=packageName.match(/^aigoni-(theme-v\d+\.\d+\.\d+)-linux-amd64\.tar\.gz$/)?.[1];assert.ok(tag);
  const info=await fs.stat(packageFile);let theme=await(await request("/api/admin/v1/theme/uploads","POST",{tag,name:packageName,size:info.size})).json();
  assert.ok(theme.id,JSON.stringify(theme));const input=await fs.open(packageFile,"r");
  try{while(theme.offset<info.size){const block=Buffer.alloc(Math.min(8*1024**2,info.size-theme.offset));const {bytesRead}=await input.read(block,0,block.length,theme.offset);assert.ok(bytesRead);const response=await fetch(origin+"/api/admin/v1/theme/uploads/"+theme.id+"?offset="+theme.offset,{method:"PUT",headers:{Cookie:cookie,"X-CSRF-Token":csrf,"Content-Type":"application/octet-stream"},body:block.subarray(0,bytesRead),signal:AbortSignal.timeout(30000)});assert.equal(response.status,200,await response.clone().text());theme=await response.json();}}finally{await input.close()}
  assert.equal((await request("/api/admin/v1/theme/uploads/"+theme.id+"/complete","POST")).status,202);
  await until(async()=>{const data=await(await request("/api/admin/v1/theme")).json();if(data.upload?.status==="failed")throw new Error(data.upload.error);return data.upload?.status==="ready";},"theme verification");
  assert.equal((await request("/api/admin/v1/theme/activate","POST",{version:tag})).status,202);
  const first=await published(0);console.log("PASS fresh-volume startup");
  assert.equal((await request("/","GET",undefined,false)).status,200);
  assert.ok((await(await request("/studio","GET",undefined,false)).text()).includes('content="/studio"'));
  assert.equal(await docker("exec","-T","aigoni","id","-u"),"1000");
  await docker("exec","-T","aigoni","node","/usr/local/lib/aigoni/healthcheck.cjs");
  const date=new Date().toISOString();
  const post=await request("/api/admin/v1/posts","POST",{title:"Docker publishing smoke",date,slug:"docker-smoke",publish:true,body:"DOCKER_PERSISTENCE_SENTINEL"});
  assert.equal(post.status,201,await post.text());
  const second=await published(first.published_revision);console.log("PASS real article publishing");
  assert.ok((await(await request("/posts/docker-smoke/","GET",undefined,false)).text()).includes("DOCKER_PERSISTENCE_SENTINEL"));
  const noteResponse=await request("/api/admin/v1/notes","POST",{title:"Private docker note",date,body:"PRIVATE_DOCKER_SENTINEL"});
  const note=await noteResponse.json();assert.equal(noteResponse.status,201,JSON.stringify(note));
  const id=note.id.split("/").map(encodeURIComponent).join("/");
  const form=new FormData();
  form.append("asset",new Blob([Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII=","base64")],{type:"image/png"}),"private.png");
  const upload=await request("/api/admin/v1/notes/"+id+"/assets","POST",form);
  const asset=await upload.json();assert.equal(upload.status,201,JSON.stringify(asset));
  assert.equal((await request(asset.path,"GET",undefined,false)).status,404);
  assert.equal((await request(asset.path)).status,200);
  await docker("up","-d","--force-recreate","--no-build","--pull","never");
  cookie="";csrf="";await login();const third=await published(second.published_revision);
  assert.notEqual(third.active,second.active,"startup must rebuild new image source");
  assert.equal((await(await request("/rest/v1/posts/docker-smoke","GET",undefined,false)).json()).title,"Docker publishing smoke");
  assert.equal((await(await request("/api/admin/v1/notes/"+id)).json()).body,"PRIVATE_DOCKER_SENTINEL");
  assert.ok(!(await(await request("/rest/v1/posts","GET",undefined,false)).text()).includes("PRIVATE_DOCKER_SENTINEL"));
  await docker("exec","-T","aigoni","node","/usr/local/lib/aigoni/healthcheck.cjs");
  console.log("PASS Docker: non-root, raw secrets, fresh-volume boot, publishing, private attachments, persistent recreation and startup rebuild");
 }catch(e){
  if(created){try{console.error(await docker("logs","--no-color","--tail=40","aigoni"));}catch{}}
  throw e;
 }finally{
  // Only this test's randomly named Compose project and its own volume.
  if(created)await docker("down","--volumes","--remove-orphans","--timeout","30");
  assert.ok(path.resolve(temp).startsWith(path.resolve(os.tmpdir())+path.sep));
  await fs.rm(temp,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e.message);process.exitCode=1;});
