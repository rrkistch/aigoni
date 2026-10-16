<script lang="ts">
 import {onMount} from "svelte";
 import {api,APIError,idPath} from "./api";
 let {kind,itemId="",onBack,onSaved}:{kind:string;itemId?:string;onBack:()=>void;onSaved:()=>void}=$props();
 let model=$state<any>({title:"",description:"",date:new Date().toISOString(),category:"",tags:[],source_url:"",body:"",slug:"",cover_image:"",toc:true,weight:0,publish:false,display:{}});
 let tags=$state("");let revision=$state("");let token=$state("");let assets=$state<any[]>([]);
 let error=$state("");let message=$state("");let busy=$state(false);let preview=$state("");let showPreview=$state(false);let dirty=$state(false);let ready=$state(false);
 let sitePreview=$state<any>(null);let previewTimer:ReturnType<typeof setInterval>|undefined;let conflict=$state(false);let remote=$state<any>(null);
 const endpoint=()=>"/"+kind+(itemId?"/"+idPath(itemId):"");
 const assetEndpoint=()=>itemId?endpoint()+"/assets":"/drafts/"+token+"/assets";
 async function load(){
  if(itemId){model=await api(endpoint());revision=model.revision;tags=model.tags.join(", ");assets=(await api(assetEndpoint())).items;}
  else token=(await api("/drafts","POST",{})).token;
  model.display||={};dirty=false;ready=true;
 }
 onMount(()=>{
  load().catch(e=>error=e.message);
  const guard=(e:BeforeUnloadEvent)=>{if(dirty){e.preventDefault();e.returnValue="";}};
  window.addEventListener("beforeunload",guard);
  const autosave=setInterval(()=>{if(ready&&dirty&&!busy&&!conflict&&!model.publish&&(model.title.trim()||kind==="notes")&&(model.body.trim()||model.title.trim())){void save();}},30000);
  return()=>{clearInterval(autosave);if(previewTimer)clearInterval(previewTimer);window.removeEventListener("beforeunload",guard);};
 });
 function back(){if(!dirty||confirm("有尚未保存的修改，确定离开？"))onBack();}
 function generateSlug(){const slug=model.title.toLowerCase().replace(/[^a-z0-9_-]+/g,"-").replace(/^-|-$/g,"");model.slug=slug||"post-"+new Date().toISOString().slice(0,10)+"-"+crypto.randomUUID().slice(0,6);dirty=true;}
 function summarize(){model.description=model.body.replace(/!\[[^\]]*\]\([^)]*\)/g,"").replace(/[#*`>_\[\]]/g,"").replace(/\s+/g," ").trim().slice(0,160);dirty=true;}
 function insert(kind:string){const snippets:Record<string,string>={heading:"\n## 小标题\n",table:"\n| 项目 | 说明 |\n| --- | --- |\n| 示例 | 内容 |\n",code:"\n```text\n代码内容\n```\n",note:"\n> [!NOTE]\n> 提示内容\n",formula:"\n$$\nE=mc^2\n$$\n"};model.body+=snippets[kind];dirty=true;}
 async function save(){
  if(kind!=="notes"&&!model.slug)generateSlug();
  busy=true;error="";message="";conflict=false;
  try{
   const body:any={title:model.title,description:model.description,date:model.date,category:model.category,tags:tags.split(/[,，]/).map(v=>v.trim()).filter(Boolean),source_url:model.source_url,body:model.body};
   if(kind!=="notes")Object.assign(body,{slug:model.slug,publish:model.publish,cover_image:model.cover_image,toc:model.toc,weight:Number(model.weight),display:model.display||{}});
   if(itemId)body.revision=revision;else body.draft_token=token;
   const sentState=JSON.stringify(model)+tags;
   const result=await api(endpoint(),itemId?"PATCH":"POST",body);
   const editedWhileSaving=JSON.stringify(model)+tags!==sentState;
   itemId=result.id;revision=result.revision;token="";
   if(!editedWhileSaving){model=result;model.display||={};tags=result.tags.join(", ");dirty=false;}else{dirty=true;}
   message=kind==="notes"?"笔记已保存":"内容已保存；网站上线进度可在概览查看";
   assets=(await api(assetEndpoint())).items;onSaved();
  }catch(e){error=(e as Error).message;if(e instanceof APIError){error+=" "+Object.values(e.fields).join("；");conflict=e.status===409;}}
  finally{busy=false;}
 }
 async function compare(){try{remote=await api(endpoint());}catch(e){error=(e as Error).message;}}
 function useRemote(){if(confirm("用服务器版本替换当前编辑内容？")){model=remote;revision=remote.revision;tags=remote.tags.join(", ");remote=null;dirty=false;conflict=false;}}
 async function render(){try{preview=(await api("/wiki/render","POST",{markdown:model.body})).html;showPreview=true;}catch(e){error=(e as Error).message;}}
 async function upload(e:Event,cover=false){
  const file=(e.currentTarget as HTMLInputElement).files?.[0];if(!file)return;
  busy=true;error="";
  try{
   const data=new FormData();data.append(cover?"cover":"asset",file);
   const a=await api(assetEndpoint()+(cover?"/cover":""),cover?"PUT":"POST",data);
   if(cover){model.cover_image=a.path;dirty=true;}
   else{model.body+="\n"+a.markdown+"\n";dirty=true;}
   assets=(await api(assetEndpoint())).items;
   // Cover operations may update the stored revision. Do not silently adopt an unrelated revision.
   if(itemId&&cover){message="封面已更新。若保存提示版本冲突，请对比服务器版本。";}
  }catch(e){error=(e as Error).message;}finally{busy=false;}
 }
 async function removeAsset(a:any){
  if(!confirm("删除附件 "+a.name+"？正文中的引用不会自动删除。"))return;
  try{await api(assetEndpoint()+"/"+(a.is_cover?"cover":encodeURIComponent(a.name)),"DELETE");assets=(await api(assetEndpoint())).items;if(a.is_cover)model.cover_image="";}
  catch(e){error=(e as Error).message;}
 }
 async function realPreview(){error="";try{if(dirty&&model.publish){error="已公开文章有未保存修改，请先明确保存后再生成预览。草稿预览不会发布文章。";return;}if(dirty||!itemId){await save();if(dirty||!itemId||error)return;}sitePreview=await api("/previews","POST",{type:kind,id:itemId});if(previewTimer)clearInterval(previewTimer);previewTimer=setInterval(async()=>{try{sitePreview=await api("/previews/"+sitePreview.id);if(["ready","failed"].includes(sitePreview.status))clearInterval(previewTimer);}catch(e){error=(e as Error).message;clearInterval(previewTimer);}},2000);}catch(e){error=(e as Error).message;}}
</script>
<section class="card-base panel stack" aria-label="内容编辑器">
 <div class="row spread"><button onclick={back}>← 返回列表</button><span class="muted">{dirty?"尚未保存":itemId?"已保存":"新建内容"}</span></div>
 <h1>{itemId?"编辑":"新建"}{kind==="notes"?"笔记":kind==="pages"?"页面":kind==="dynamics"?"动态":kind==="projects"?"项目":"文章"}</h1>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if conflict}<div class="notice"><p>服务器内容已发生变化。你的编辑内容仍保留，请先对比，不会强制覆盖。</p><button onclick={compare}>查看服务器版本</button></div>{/if}
 {#if remote}<details open><summary>服务器版本</summary><pre class="editor">{remote.body}</pre><button onclick={useRemote}>采用服务器版本</button></details>{/if}
 {#if ready}
 <form class="stack" onsubmit={(e)=>{e.preventDefault();save();}} oninput={()=>dirty=true}>
  <div class="fields">
   <label class="full">标题<input bind:value={model.title} required={kind!=="notes"} /></label>
   <label class="full">摘要<textarea rows="2" bind:value={model.description}></textarea></label><button type="button" onclick={summarize}>从正文提取摘要（不使用 AI）</button>
   <label>日期（含时区）<input bind:value={model.date} placeholder="2026-10-16T12:00:00+08:00" required /></label>
   {#if kind!=="notes"}<label>链接标识<input bind:value={model.slug} pattern="[A-Za-z0-9_-]+" placeholder="留空自动生成" /></label><button type="button" onclick={generateSlug}>生成链接名称</button>{/if}
   <label>分类<input bind:value={model.category} /></label>
   <label>标签<input bind:value={tags} placeholder="用逗号分隔" /></label>
   <label class="full">来源链接<input type="url" bind:value={model.source_url} placeholder="收藏网页的原始地址" /></label>
   {#if kind!=="notes"}
    <label>排序权重<input type="number" bind:value={model.weight} /></label>
    <label>封面地址<input bind:value={model.cover_image} /></label>
    <label class="row"><input type="checkbox" bind:checked={model.publish} />公开发布</label>
    <label class="row"><input type="checkbox" bind:checked={model.toc} />显示目录</label>
   {/if}
  </div>
  {#if kind!=="notes"}<details><summary>系列、评论与展示设置</summary><div class="fields">
 <label>所属系列<input bind:value={model.display.series} /></label><label>系列顺序<input type="number" min="0" bind:value={model.display.seriesOrder} /></label>
 <label class="row"><input type="checkbox" checked={model.display.comment??true} onchange={(e)=>model.display.comment=e.currentTarget.checked} />允许评论</label>
 <label class="row"><input type="checkbox" bind:checked={model.display.pinned} />置顶</label>
 <label>许可名称<input bind:value={model.display.licenseName} /></label><label>许可地址<input bind:value={model.display.licenseUrl} /></label>
 {#if kind==="dynamics"}<label>地点<input bind:value={model.display.location} /></label>{/if}
 {#if kind==="projects"}<label>项目状态<input bind:value={model.display.status} placeholder="进行中 / 已完成" /></label>{/if}
 </div></details>{/if}
 <div class="row spread"><h2>Markdown 正文</h2><button type="button" onclick={()=>showPreview?showPreview=false:render()}>{showPreview?"继续编辑":"预览"}</button></div>
  <div class="row"><button type="button" onclick={()=>insert("heading")}>标题</button><button type="button" onclick={()=>insert("table")}>表格</button><button type="button" onclick={()=>insert("code")}>代码块</button><button type="button" onclick={()=>insert("note")}>提示框</button><button type="button" onclick={()=>insert("formula")}>公式</button></div>
  {#if showPreview}<div class="prose dark:prose-invert">{@html preview}</div>{:else}<label>正文<textarea class="editor" bind:value={model.body}></textarea></label>{/if}
  <div class="row"><button class="primary" disabled={busy} type="submit">{busy?"正在保存…":"保存内容"}</button><span class="muted">{kind==="notes"?"仅自己可见":"草稿每 30 秒自动保存；已公开内容须手动保存"}</span></div>
 </form>
  {#if kind!=="notes"}<div class="row"><button disabled={busy||sitePreview?.status==="queued"||sitePreview?.status==="building"} onclick={realPreview}>保存并生成 Firefly 预览</button>{#if sitePreview?.status==="ready"}<a href={sitePreview.url} target="_blank" rel="noreferrer" data-no-swup>打开预览 ↗</a>{:else if sitePreview?.status==="failed"}<p role="alert">{sitePreview.error}</p>{:else if sitePreview}<p role="status">正在生成登录态预览…</p>{/if}</div>{/if}
 <details><summary>附件与封面（{assets.length}）</summary>
  <div class="row"><label class="upload">上传附件<input type="file" disabled={busy} onchange={(e)=>upload(e)} /></label>
  {#if kind!=="notes"}<label class="upload">上传封面<input type="file" accept="image/*" disabled={busy} onchange={(e)=>upload(e,true)} /></label>{/if}</div>
  {#each assets as a}<div class="item"><a class="grow" href={a.path} target="_blank" rel="noreferrer">{a.name}</a><button onclick={()=>{model.body+="\n"+a.markdown+"\n";dirty=true;}}>插入正文</button><button onclick={()=>removeAsset(a)}>删除</button></div>{/each}
 </details>
 {/if}
</section>
