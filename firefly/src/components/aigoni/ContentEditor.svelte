<script lang="ts">
 import {onMount} from "svelte";
 import {api,APIError,idPath} from "./api";
 let {kind,itemId="",onBack,onSaved}:{kind:string;itemId?:string;onBack:()=>void;onSaved:()=>void}=$props();
 let model=$state<any>({title:"",description:"",date:new Date().toISOString(),category:"",tags:[],source_url:"",body:"",slug:"",cover_image:"",toc:true,weight:0,publish:false});
 let tags=$state("");let revision=$state("");let token=$state("");let assets=$state<any[]>([]);
 let error=$state("");let message=$state("");let busy=$state(false);let preview=$state("");let showPreview=$state(false);let dirty=$state(false);let ready=$state(false);
 let conflict=$state(false);let remote=$state<any>(null);
 const endpoint=()=>"/"+kind+(itemId?"/"+idPath(itemId):"");
 const assetEndpoint=()=>itemId?endpoint()+"/assets":"/drafts/"+token+"/assets";
 async function load(){
  if(itemId){model=await api(endpoint());revision=model.revision;tags=model.tags.join(", ");assets=(await api(assetEndpoint())).items;}
  else token=(await api("/drafts","POST",{})).token;
  dirty=false;ready=true;
 }
 onMount(()=>{
  load().catch(e=>error=e.message);
  const guard=(e:BeforeUnloadEvent)=>{if(dirty){e.preventDefault();e.returnValue="";}};
  window.addEventListener("beforeunload",guard);
  return()=>window.removeEventListener("beforeunload",guard);
 });
 function back(){if(!dirty||confirm("有尚未保存的修改，确定离开？"))onBack();}
 async function save(){
  busy=true;error="";message="";conflict=false;
  try{
   const body:any={title:model.title,description:model.description,date:model.date,category:model.category,tags:tags.split(/[,，]/).map(v=>v.trim()).filter(Boolean),source_url:model.source_url,body:model.body};
   if(kind!=="notes")Object.assign(body,{slug:model.slug,publish:model.publish,cover_image:model.cover_image,toc:model.toc,weight:Number(model.weight)});
   if(itemId)body.revision=revision;else body.draft_token=token;
   const result=await api(endpoint(),itemId?"PATCH":"POST",body);
   itemId=result.id;model=result;revision=result.revision;tags=result.tags.join(", ");dirty=false;token="";
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
</script>
<section class="card-base panel stack" aria-label="内容编辑器">
 <div class="row spread"><button onclick={back}>← 返回列表</button><span class="muted">{dirty?"尚未保存":itemId?"已保存":"新建内容"}</span></div>
 <h1>{itemId?"编辑":"新建"}{kind==="notes"?"笔记":kind==="pages"?"页面":"文章"}</h1>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if conflict}<div class="notice"><p>服务器内容已发生变化。你的编辑内容仍保留，请先对比，不会强制覆盖。</p><button onclick={compare}>查看服务器版本</button></div>{/if}
 {#if remote}<details open><summary>服务器版本</summary><pre class="editor">{remote.body}</pre><button onclick={useRemote}>采用服务器版本</button></details>{/if}
 {#if ready}
 <form class="stack" onsubmit={(e)=>{e.preventDefault();save();}} oninput={()=>dirty=true}>
  <div class="fields">
   <label class="full">标题<input bind:value={model.title} required={kind!=="notes"} /></label>
   <label class="full">摘要<textarea rows="2" bind:value={model.description}></textarea></label>
   <label>日期（含时区）<input bind:value={model.date} placeholder="2026-10-16T12:00:00+08:00" required /></label>
   {#if kind!=="notes"}<label>链接标识<input bind:value={model.slug} required pattern="[A-Za-z0-9_-]+" /></label>{/if}
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
  <div class="row spread"><h2>Markdown 正文</h2><button type="button" onclick={()=>showPreview?showPreview=false:render()}>{showPreview?"继续编辑":"预览"}</button></div>
  {#if showPreview}<div class="prose dark:prose-invert">{@html preview}</div>{:else}<label>正文<textarea class="editor" bind:value={model.body}></textarea></label>{/if}
  <div class="row"><button class="primary" disabled={busy} type="submit">{busy?"正在保存…":"保存内容"}</button><span class="muted">{kind==="notes"?"仅自己可见":"未勾选公开发布时，保存为草稿"}</span></div>
 </form>
 <details><summary>附件与封面（{assets.length}）</summary>
  <div class="row"><label class="upload">上传附件<input type="file" disabled={busy} onchange={(e)=>upload(e)} /></label>
  {#if kind!=="notes"}<label class="upload">上传封面<input type="file" accept="image/*" disabled={busy} onchange={(e)=>upload(e,true)} /></label>{/if}</div>
  {#each assets as a}<div class="item"><a class="grow" href={a.path} target="_blank" rel="noreferrer">{a.name}</a><button onclick={()=>{model.body+="\n"+a.markdown+"\n";dirty=true;}}>插入正文</button><button onclick={()=>removeAsset(a)}>删除</button></div>{/each}
 </details>
 {/if}
</section>
