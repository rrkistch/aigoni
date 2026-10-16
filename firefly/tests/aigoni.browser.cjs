const assert=require("node:assert/strict");
const fs=require("node:fs");const path=require("node:path");const http=require("node:http");
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||"playwright");
const root=path.resolve(process.env.FIREFLY_TEST_RELEASE||"../.firefly/releases/preview");
const schema=JSON.parse(fs.readFileSync(path.resolve("../internal/server/admin_schema.json"),"utf8"));
const defaults=JSON.parse(fs.readFileSync(path.resolve("aigoni-defaults.json"),"utf8"));
let themeRevision=0,themeGroups={};
let aiState={revision:0,default_provider:"",source:"environment",providers:[]};
const evidence=path.resolve(process.env.FIREFLY_TEST_EVIDENCE||"../.firefly/evidence");
(async()=>{
 assert.ok(fs.existsSync(path.join(root,"_workspace/index.html")),"Build an integration release first");
 const server=http.createServer((req,res)=>{
  let url=new URL(req.url,"http://localhost").pathname;
  url=url.replace(/^\/_firefly\/[^/]+/,"");
  if(url.startsWith("/studio"))url="/_workspace/index.html";
  let file=path.resolve(root,"."+decodeURIComponent(url));
  if(!file.startsWith(root+path.sep)&&file!==root){res.writeHead(404);res.end();return;}
  if(fs.existsSync(file)&&fs.statSync(file).isDirectory())file=path.join(file,"index.html");
  if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}
  const types={".html":"text/html",".js":"text/javascript",".css":"text/css",".svg":"image/svg+xml",".json":"application/json",".woff2":"font/woff2"};
  res.setHeader("Content-Type",types[path.extname(file)]||"application/octet-stream");
  const data=fs.readFileSync(file);
  res.end(url==="/_workspace/index.html"?data.toString().replace("</head>",'<meta name="aigoni-admin-path" content="/studio"></head>'):data);
 });
 await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));const origin="http://127.0.0.1:"+server.address().port;
 const executablePath=process.env.PLAYWRIGHT_BROWSER||["C:/Program Files/Google/Chrome/Application/chrome.exe","C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"].find(p=>fs.existsSync(p));
 const browser=await chromium.launch({headless:true,...(executablePath?{executablePath}:{}),timeout:60000});
 try {
  const page=await browser.newPage({viewport:{width:1440,height:1000}});
  const app=page.locator(".aigoni");
  let authenticated=false;let revision="r1";let conflict=false;let saved=false;let posts=[];
  const errors=[];page.on("pageerror",e=>errors.push(e.message));
  await page.route("**/api/admin/v1/**",async route=>{
   const req=route.request();const p=new URL(req.url()).pathname.replace("/api/admin/v1","");
   const method=req.method();let status=200;let data={};
   if(p==="/session"&&method==="POST"){authenticated=true;data={authenticated:true,csrf_token:"csrf-test"};}
   else if(!authenticated){status=401;data={error:{message:"请登录"}};}
   else if(method!=="GET"&&req.headers()["x-csrf-token"]!=="csrf-test"){status=403;data={error:{message:"CSRF"}};}
   else if(p==="/session"){if(method==="DELETE"){authenticated=false;status=204;}else data={authenticated:true,csrf_token:"csrf-test"};}
   else if(p==="/dashboard")data={posts:{total:posts.length,published:posts.length,draft:0},pages:{total:0,draft:0},notes:1};
   else if(p==="/site-build")data={enabled:true,status:method==="POST"?"queued":"published",revision:2,published_revision:2};
   else if(p==="/theme-settings"){
    if(method==="PATCH"){const body=req.postDataJSON();assert.equal(body.revision,themeRevision);Object.assign(themeGroups,body.groups);themeRevision++;}
    data={revision:themeRevision,groups:themeGroups,effective:{...defaults,...themeGroups},defaults,schema,saved:true,build_status:"queued"};
   }
   else if(p==="/media")data={items:[]};
   else if(p==="/ai/providers"){
    if(method==="PATCH"){const body=req.postDataJSON();aiState={...body,revision:body.revision+1,source:body.default_provider?"backend":"environment",providers:body.providers.map(p=>({...p,id:p.id||"provider-fixture",has_key:true,api_key:undefined}))};}
    data=aiState;
   }
   else if(p==="/ai/providers/provider-fixture/models")data={models:["fixture-model"]};
   else if(p==="/drafts")data={token:"fixture-token"};
   else if(p.endsWith("/assets"))data={items:[]};
   else if(["/categories","/tags","/note-categories","/note-tags"].includes(p))data=[];
   else if(p==="/posts"&&method==="GET")data={items:posts,total:posts.length,total_pages:1};
   else if(p==="/notes")data={items:[{id:"2026/private",title:"PRIVATE_SENTINEL",date:"2026-10-16T00:00:00Z"}],total:1,total_pages:1};
   else if(p==="/posts"&&method==="POST"){
    const body=req.postDataJSON();assert.equal(body.slug,"my-first-post");assert.equal(body.draft_token,"fixture-token");
    data={...body,id:"2026/first",revision,tags:body.tags};posts=[data];saved=true;status=201;
   }else if(p==="/posts/2026/first"&&method==="PATCH"){
    assert.equal(req.postDataJSON().revision,"r1");
    status=409;conflict=true;data={error:{code:"revision_conflict",message:"服务器版本已变化"}};
   }else if(p==="/posts/2026/first")data={...posts[0],revision:"r2",body:"服务器上的新版本"};
   else if(p==="/wiki/documents")data={items:[{path:"wiki/index.md"}]};
   else if(p==="/wiki/documents/content")data={path:"wiki/index.md",html:"<h2>我的知识索引</h2><p>PRIVATE_SENTINEL</p>"};
   else if(p==="/wiki/render")data={html:"<p>预览正文</p>"};
   else if(p==="/wiki/chat")data={id:"run-fixture",status:"pending"};
   else if(p==="/wiki/runs/run-fixture/events"){
    await route.fulfill({status:200,contentType:"text/event-stream",body:'event: delta\ndata: {"delta":"模拟回答","answer":"模拟回答"}\n\nevent: done\ndata: {}\n\n'});return;
   }else if(p==="/wiki/runs/run-fixture")data={id:"run-fixture",status:"completed",answer:"模拟回答",answer_html:"<p>模拟回答</p>",events:[]};
   else {status=404;data={error:{message:"Unmocked "+p}};}
   await route.fulfill({status,contentType:"application/json",body:status===204?"":JSON.stringify(data)});
  });
  await page.goto(origin+"/",{waitUntil:"domcontentloaded",timeout:120000});
  const results=await page.evaluate(async()=>{
   const index=await import("/pagefind/pagefind.js");
   const found=await index.search("知识");
   return await Promise.all(found.results.map(async result=>(await result.data()).url));
  });
  assert.ok(results.some(url=>url.includes("/posts/hello/")),"public Pagefind search must find the exported article");
  assert.ok(results.every(url=>!url.includes("_workspace")),"private shell must not be indexed");
  await page.goto(origin+"/studio",{waitUntil:"domcontentloaded",timeout:120000});
  await app.getByLabel("管理员密码").fill("fixture-only-password");
  await app.getByRole("button",{name:"进入工作区",exact:true}).click();
  await app.getByRole("heading",{name:"私人工作区",exact:true}).waitFor();
  await app.getByRole("button",{name:"文章",exact:true}).click();
  await app.getByRole("button",{name:"＋ 新建",exact:true}).click();
  await app.getByLabel("标题",{exact:true}).fill("第一篇文章");
  await app.getByLabel("链接标识").fill("my-first-post");
  await app.getByLabel("正文",{exact:true}).fill("本地测试正文");
  await app.getByRole("button",{name:"保存内容",exact:true}).click();
  await app.getByRole("status").filter({hasText:"内容已保存"}).waitFor();assert.ok(saved);
  await app.getByLabel("正文",{exact:true}).fill("我的修改不能丢");
  await app.getByRole("button",{name:"保存内容",exact:true}).click();
  await app.getByRole("button",{name:"查看服务器版本",exact:true}).waitFor();assert.ok(conflict);
  assert.equal(await app.getByLabel("正文",{exact:true}).inputValue(),"我的修改不能丢");
  page.on("dialog",d=>d.accept());
  await app.getByRole("button",{name:"Wiki",exact:true}).click();
  await app.getByRole("button",{name:"index.md",exact:true}).click();
  await app.getByRole("heading",{name:"我的知识索引",exact:true}).waitFor();
  await app.getByRole("button",{name:"AI 对话",exact:true}).click();
  await app.getByLabel("消息",{exact:true}).fill("整理知识库");
  await app.getByRole("button",{name:"发送",exact:true}).click();
  await app.getByText("模拟回答",{exact:true}).waitFor();
  fs.mkdirSync(evidence,{recursive:true});
  for(const [label,width,dark] of [["desktop-light",1440,false],["mobile-light",390,false],["desktop-dark",1440,true],["mobile-dark",390,true]]){
   await page.setViewportSize({width,height:1000});
   await page.evaluate(d=>document.documentElement.classList.toggle("dark",d),dark);
   await page.evaluate(()=>window.scrollTo(0,0));await page.waitForTimeout(300);
   await page.screenshot({path:path.join(evidence,"workspace-"+label+".png")});
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),"horizontal overflow");
  }
  await page.setViewportSize({width:1440,height:1000});
  await app.getByRole("button",{name:"AI 服务",exact:true}).click();
  await app.getByRole("button",{name:"＋ 添加服务",exact:true}).click();
  await app.getByLabel("服务名称",{exact:true}).fill("Mock service");
  await app.getByLabel("API 基础地址",{exact:true}).fill("https://example.invalid/v1");
  await app.getByLabel("API Key",{exact:true}).fill("fixture-key");
  await app.getByRole("button",{name:"保存连接",exact:true}).click();
  await app.getByRole("button",{name:"获取模型列表",exact:true}).click();
  await app.getByLabel("可用模型",{exact:true}).selectOption("fixture-model");
  await app.getByRole("button",{name:"保存模型选择",exact:true}).click();
  await app.getByLabel("知识库默认连接",{exact:true}).selectOption("provider-fixture");
  await app.getByText("默认连接已更新，新任务立即生效；进行中的任务不受影响。",{exact:true}).waitFor();
  await page.screenshot({path:path.join(evidence,"ai-settings.png")});
  await app.getByRole("button",{name:"外观与模块",exact:true}).click();
  await app.getByLabel("色相",{exact:true}).fill("220");
  await app.getByRole("button",{name:"保存并生成网站",exact:true}).click();
  await app.getByText("设置已保存，请到概览查看网站上线状态。",{exact:true}).waitFor();
  assert.equal(themeGroups.appearance.themeColor.hue,220);
  await app.getByLabel("选择要管理的区域",{exact:true}).selectOption("music");
  await app.getByLabel("歌名",{exact:true}).first().fill("我的歌曲");
  await app.getByRole("button",{name:"保存并生成网站",exact:true}).click();
  await app.getByText("设置已保存，请到概览查看网站上线状态。",{exact:true}).waitFor();
  for(const [label,width,dark] of [["desktop",1440,false],["mobile",390,true]]){
   await page.setViewportSize({width,height:1000});await page.evaluate(d=>document.documentElement.classList.toggle("dark",d),dark);
   await page.screenshot({path:path.join(evidence,"theme-settings-"+label+".png")});
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),"settings overflow");
  }
  await app.getByRole("button",{name:"退出",exact:true}).click();
  await app.getByLabel("管理员密码").waitFor();assert.equal(authenticated,false);
  assert.deepEqual(errors,[]);
  console.log("PASS public search, login, CSRF, create, conflict retention, Wiki, AI stream, responsive themes, logout");
 }catch(error){const current=browser.contexts()[0]?.pages()[0];if(current){fs.mkdirSync(evidence,{recursive:true});await current.screenshot({path:path.join(evidence,"browser-failure.png")});console.error((await current.locator(".aigoni").innerText()).slice(-5000));}throw error;}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(e=>{console.error(e);process.exit(1);});
