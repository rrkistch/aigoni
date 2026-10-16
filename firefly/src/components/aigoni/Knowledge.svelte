<script lang="ts">
 import {onMount} from "svelte";
 import {api,eventStream} from "./api";
 let providers=$state<any[]>([]);let providerID=$state("");let modelID=$state("");let modelOptions=$state<string[]>([]);
 let {mode}:{mode:"wiki"|"chat"}=$props();
 let docs=$state<{path:string}[]>([]);let selected=$state("");let html=$state("");let query=$state("");
 let prompt=$state("");let answer=$state("");let answerHTML=$state("");let run=$state<any>(null);
 let messages=$state<{role:string;content:string}[]>([]);let tools=$state<any[]>([]);let error=$state("");let busy=$state(false);let connected=$state(false);
 let controller:AbortController|undefined;let timer:ReturnType<typeof setInterval>|undefined;let disposed=false;
 const key=()=> "aigoni-run:"+ (document.querySelector('meta[name="aigoni-admin-path"]')?.getAttribute("content")||"/admin");
 const active=()=>run&&["pending","running"].includes(run.status);
 async function readDoc(path:string){try{const d=await api("/wiki/documents/content?path="+encodeURIComponent(path));selected=path;html=d.html;}catch(e){error=(e as Error).message;}}
 async function loadDocs(){docs=(await api("/wiki/documents")).items;}
 async function refreshRun(){
  if(!run)return;
  try{
   const result=await api("/wiki/runs/"+encodeURIComponent(run.id));run=result;
   if(result.answer)answer=result.answer;
   if(result.answer_html)answerHTML=result.answer_html;
   tools=(result.events||[]).filter((e:any)=>e.type==="tool"||e.tool);
   if(!active()){
    controller?.abort();connected=false;if(timer)clearInterval(timer);
    sessionStorage.removeItem(key());
    if(result.status==="failed")error=result.error||"任务未完成";
   }
  }catch(e){error=(e as Error).message;if(timer)clearInterval(timer);controller?.abort();}
 }
 async function connect(){
  if(!run||disposed)return;controller?.abort();controller=new AbortController();connected=true;
  try{
   await eventStream(run.id,0,controller.signal,(name,data)=>{
    if(name==="delta"||data.type==="delta"){answer=data.answer||answer+(data.delta||"");}
    if(name==="done"||name==="error")void refreshRun();
   });
  }catch(e){if((e as Error).name!=="AbortError")error="实时连接断开，正在通过任务状态恢复；也可重新连接。";}
  finally{connected=false;}
 }
 async function start(){
  if(!prompt.trim()||active())return;error="";busy=true;answer="";answerHTML="";tools=[];
  const question=prompt.trim();prompt="";
  try{
   run=await api("/wiki/chat","POST",{message:question,history:messages.slice(-20),...(providerID?{provider_id:providerID,model:modelID}:{})});
   messages=[...messages,{role:"user",content:question}];sessionStorage.setItem(key(),run.id);
   void connect();if(timer)clearInterval(timer);timer=setInterval(refreshRun,1500);
  }catch(e){error=(e as Error).message;prompt=question;}finally{busy=false;}
 }
 async function cancel(){try{await api("/wiki/runs/"+run.id+"/cancel","POST");await refreshRun();}catch(e){error=(e as Error).message;}}
 function nextQuestion(){if(answer){messages=[...messages,{role:"assistant",content:answer}];answer="";answerHTML="";run=null;tools=[];}}
 onMount(()=>{
  if(mode==="chat")void api("/ai/providers").then(data=>providers=data.providers||[]).catch(e=>error=e.message);
  loadDocs().catch(e=>error=e.message);
  const id=sessionStorage.getItem(key());
  if(id){run={id,status:"pending"};void refreshRun().then(()=>{if(active()){void connect();timer=setInterval(refreshRun,1500);}});}
  return()=>{disposed=true;controller?.abort();if(timer)clearInterval(timer);};
 });
 function docLink(event:MouseEvent){
  const anchor=(event.target as HTMLElement).closest("a");if(!anchor)return;
  const href=anchor.getAttribute("href")||"";
  if(/^(https?:|mailto:|#)/.test(href))return;
  const dir=selected.slice(0,selected.lastIndexOf("/")+1);
  let path=href.startsWith("wiki/")||href.startsWith("content/notes/")?href:new URL(href,"https://local/"+dir).pathname.slice(1);
  if(path.startsWith("wiki/")||path.startsWith("content/notes/")){event.preventDefault();void readDoc(decodeURIComponent(path.split("#")[0]));}
 }
</script>
<section class="card-base panel stack">
 <h1>{mode==="wiki"?"我的知识库":"和知识库对话"}</h1>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if mode==="wiki"}
  <label>查找文档<input type="search" bind:value={query} placeholder="输入文档路径或名称" /></label>
  <div class="row">{#each docs.filter(d=>d.path.toLowerCase().includes(query.toLowerCase())) as d}<button onclick={()=>readDoc(d.path)} aria-pressed={selected===d.path}>{d.path.replace(/^wiki\//,"")}</button>{/each}</div>
  {#if !docs.length}<p class="muted">还没有 Wiki 文档。先保存笔记，再在 AI 对话中让它整理知识库。</p>{/if}
  {#if selected}<h2>{selected}</h2><div class="prose dark:prose-invert" onclick={docLink} role="presentation">{@html html}</div>{/if}
 {:else}
  <p class="muted">可以提问，也可以让 AI 整理笔记、更新 Wiki 或检查知识库。需要在服务器配置模型。</p>
  {#each messages as m}<div class="message" class:user={m.role==="user"}><strong>{m.role==="user"?"我":"AI"}</strong><p style="white-space:pre-wrap">{m.content}</p></div>{/each}
  {#if run}
   <div class="row spread"><span class="pill">{run.status==="completed"?"已完成":run.status==="failed"?"已停止":connected?"正在回答":"任务处理中"}</span>
    {#if active()}<div class="row"><button onclick={connect}>重新连接</button><button onclick={cancel}>停止任务</button></div>{/if}
   </div>
   <div class="message prose dark:prose-invert" aria-live="polite">{#if answerHTML}{@html answerHTML}{:else}<p style="white-space:pre-wrap">{answer||"正在检索与整理…"}</p>{/if}</div>
   {#if run.sources?.length}<details><summary>参考来源</summary>{#each run.sources as source}<p>{typeof source==="string"?source:JSON.stringify(source)}</p>{/each}</details>{/if}
   {#if tools.length}<details><summary>工具记录（{tools.length}）</summary>{#each tools as tool}<p>{tool.tool} · {tool.summary||tool.status}</p>{/each}</details>{/if}
   {#if !active()}<button onclick={nextQuestion}>继续追问</button>{/if}
  {/if}
  {#if mode==="chat"}<div class="fields"><label>本次对话的模型连接<select disabled={active()} bind:value={providerID} onchange={()=>{modelID=providers.find(p=>p.id===providerID)?.model||"";modelOptions=[];}}><option value="">使用知识库默认配置</option>{#each providers as p}<option value={p.id}>{p.name}</option>{/each}</select></label>{#if providerID}<label>模型 ID<input disabled={active()} bind:value={modelID} list="chat-model-options" /><datalist id="chat-model-options">{#each modelOptions as m}<option value={m}></option>{/each}</datalist></label><button type="button" disabled={active()} onclick={()=>api(`/ai/providers/${providerID}/models`,"POST").then(data=>modelOptions=data.models).catch(e=>error=e.message)}>获取模型</button>{/if}</div>{/if}
 <form class="stack" onsubmit={(e)=>{e.preventDefault();start();}}>
   <label>消息<textarea rows="4" bind:value={prompt} placeholder="例如：把我的笔记整理到 Wiki，并说明更新了什么。"></textarea></label>
   <div class="row"><button class="primary" disabled={busy||!!active()||!prompt.trim()}>发送</button>
   <button type="button" disabled={!!active()} onclick={()=>{messages=[];answer="";answerHTML="";run=null;}}>新对话</button></div>
  </form>
 {/if}
</section>
