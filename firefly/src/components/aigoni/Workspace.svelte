<script lang="ts">
 import {onMount} from "svelte";
 import {api,setCSRF} from "./api";
 import ContentEditor from "./ContentEditor.svelte";
 import Knowledge from "./Knowledge.svelte";
 import Settings from "./Settings.svelte";
 import AISettings from "./AISettings.svelte";
 import ThemeSettings from "./ThemeSettings.svelte";
 import MediaLibrary from "./MediaLibrary.svelte";
 import Onboarding from "./Onboarding.svelte";
 import Maintenance from "./Maintenance.svelte";
 import "./workspace.css";
 let authenticated=$state(false);let checking=$state(true);let password=$state("");let error=$state("");let busy=$state(false);
 let section=$state("overview");let list=$state<any>({items:[],total:0,total_pages:0});let page=$state(1);let query=$state("");let category=$state("");let tag=$state("");let published=$state("");
 let categories=$state<any[]>([]);let tags=$state<any[]>([]);let editing=$state(false);let itemId=$state("");let dashboard=$state<any>(null);let build=$state<any>(null);
 let request=0;let timer:ReturnType<typeof setInterval>;let disposed=false;
 const tabs=[["overview","概览"],["posts","文章"],["pages","页面"],["dynamics","动态"],["projects","项目"],["notes","笔记与收藏"],["wiki","Wiki"],["chat","AI 对话"],["settings","设置"],["ai-settings","AI 服务"],["appearance","外观与模块"],["media","素材"],["setup","建站引导"],["maintenance","网站维护"]];
 const contentSection=()=>["posts","pages","notes","dynamics","projects"].includes(section);
 const title=()=>tabs.find(t=>t[0]===section)?.[1]||"工作区";
 async function refreshStatus(){if(authenticated){[dashboard,build]=await Promise.all([api("/dashboard"),api("/site-build")]);}}
 async function offerSetup(){if(dashboard?.features?.onboarding&&!location.hash){try{const state=await api("/onboarding");if(state.suggested)await navigate("setup");}catch{error="建站引导暂时不可用，可以继续使用其他功能。";}}}
 async function check(){try{const session=await api("/session");setCSRF(session.csrf_token);authenticated=true;await refreshStatus();const initial=location.hash.slice(1)||location.pathname.split("/")[2];if(tabs.some(t=>t[0]===initial))await navigate(initial);else await offerSetup();}catch{authenticated=false;error="";}finally{checking=false;}}
 async function login(){busy=true;error="";try{const session=await api("/session","POST",{password});setCSRF(session.csrf_token);password="";authenticated=true;await refreshStatus();await offerSetup();}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function logout(){try{await api("/session","DELETE");setCSRF("");authenticated=false;list={items:[]};dashboard=null;editing=false;section="overview";sessionStorage.removeItem("aigoni-run:"+(document.querySelector('meta[name="aigoni-admin-path"]')?.getAttribute("content")||"/admin"));}catch(e){error=(e as Error).message;}}
 async function loadList(){
  const serial=++request;busy=true;error="";
  const params=new URLSearchParams({page:String(page),per_page:"20"});
  let endpoint="/"+section;
  if(query.trim()&&!["dynamics","projects"].includes(section)){endpoint="/search";params.set("q",query.trim());params.set("type",section.slice(0,-1));}
  else{if(category)params.set("category",category);if(tag)params.set("tag",tag);if(published&&section!=="notes")params.set("publish",published);}
  try{
   if(query.trim())params.set("q",query.trim());
   const result=await api(endpoint+"?"+params);
   if(serial===request&&!disposed)list=result;
  }catch(e){if(serial===request)error=(e as Error).message;}finally{if(serial===request)busy=false;}
 }
 async function navigate(next:string){
  if(editing&&!confirm("离开编辑器？请确认内容已保存。"))return;
  section=next;history.replaceState(history.state,"","#"+next);editing=false;query="";category="";tag="";published="";page=1;error="";request++;
  if(contentSection()){
   const prefix=next==="notes"?"/note-":"/";
   try{[categories,tags]=await Promise.all([api(prefix+"categories"),api(prefix+"tags")]);await loadList();}catch(e){error=(e as Error).message;}
  }else if(next==="overview")await refreshStatus();
 }
 async function rebuild(){try{build=await api("/site-build","POST");}catch(e){error=(e as Error).message;}}
 async function remove(item:any){
  if(!confirm("删除「"+(item.title||"无标题笔记")+"」？此操作不能撤销。"))return;
  try{await api("/"+section+"/"+item.id.split("/").map(encodeURIComponent).join("/"),"DELETE",undefined,{"If-Match":'"'+item.revision+'"'});await loadList();await refreshStatus();}
  catch(e){error=(e as Error).message;}
 }
 function expired(){authenticated=false;setCSRF("");error="登录已过期，请重新登录。";}
 onMount(()=>{
  void check();timer=setInterval(()=>refreshStatus().catch(()=>{}),3000);
  window.addEventListener("aigoni-session-expired",expired);
  return()=>{disposed=true;request++;clearInterval(timer);window.removeEventListener("aigoni-session-expired",expired);};
 });
 const buildLabel=()=>({disabled:"未启用",idle:"待首次构建",queued:"等待构建",building:"正在构建",published:"已上线",failed:"构建失败"}[build?.status as string]||"读取中");
</script>
<div class="aigoni text-90">
 {#if checking}<section class="card-base panel"><p role="status">正在确认登录状态…</p></section>
 {:else if !authenticated}
 <section class="card-base panel stack">
  <div><p class="muted">AIGONI · PRIVATE SPACE</p><h1>回到你的知识世界</h1></div>
  <p class="muted">登录后管理文章、笔记与 Wiki。</p>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  <form class="stack" onsubmit={(e)=>{e.preventDefault();login();}}><label>管理员密码<input type="password" autocomplete="current-password" bind:value={password} required /></label><button class="primary" disabled={busy}>{busy?"登录中…":"进入工作区"}</button></form>
 </section>
 {:else}
 <header class="card-base panel row spread"><div><p class="muted">只属于你的空间</p><h1>私人工作区</h1></div><div class="row"><a class="button" href="/" data-no-swup>查看博客 ↗</a><button onclick={logout}>退出</button></div></header>
 <nav class="card-base tabs" aria-label="工作区导航">{#each tabs as tab}<button aria-current={section===tab[0]?"page":undefined} onclick={()=>navigate(tab[0])}>{tab[1]}</button>{/each}</nav>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if section==="overview"}
  <section class="card-base panel stack"><h2>内容概览</h2>
   {#if dashboard}<div class="stats"><div class="stat"><span>文章</span><strong>{dashboard.posts.total}</strong><small>{dashboard.posts.published} 篇已发布</small></div><div class="stat"><span>页面</span><strong>{dashboard.pages.total}</strong><small>{dashboard.pages.draft} 篇草稿</small></div><div class="stat"><span>私人笔记</span><strong>{dashboard.notes}</strong><small>仅自己可见</small></div></div>{/if}
   <div class="row"><button onclick={()=>navigate("posts")}>管理文章</button><button onclick={()=>navigate("notes")}>整理笔记</button><button onclick={()=>navigate("chat")}>向知识库提问</button></div>
  </section>
  <section class="card-base panel stack"><div class="row spread"><h2>博客发布</h2><span class="pill" role="status">{buildLabel()}</span></div>
   {#if build}<p class="muted">内容版本 {build.revision} · 已上线版本 {build.published_revision}</p>
   {#if build.blocked}<div class="notice">已撤回内容的旧公开版本已停供，等待安全的新版本完成。</div>{/if}
   {#if build.error}<div class="notice error">{build.error}</div>{/if}
   <button disabled={!build.enabled||build.status==="building"} onclick={rebuild}>{build.status==="failed"?"重试构建":"重新构建网站"}</button>{/if}
  </section>
 {:else if contentSection()}
  {#if editing}
   {#key section+itemId}<ContentEditor kind={section} {itemId} onBack={()=>{editing=false;loadList();}} onSaved={()=>refreshStatus()} />{/key}
  {:else}
   <section class="card-base panel stack">
    <div class="row spread"><h1>{title()}</h1><button class="primary" onclick={()=>{itemId="";editing=true;}}>＋ 新建</button></div>
    <form class="stack" onsubmit={(e)=>{e.preventDefault();page=1;loadList();}}>
     <label>搜索<input type="search" bind:value={query} placeholder="搜索标题与正文" /></label>
     <div class="fields"><label>分类<select bind:value={category} disabled={!!query}><option value="">全部分类</option>{#each categories as c}<option value={c.none?"__none__":c.name}>{c.name} ({c.count})</option>{/each}</select></label>
     <label>标签<select bind:value={tag} disabled={!!query}><option value="">全部标签</option>{#each tags as t}<option value={t.name}>{t.name}</option>{/each}</select></label>
     {#if section!=="notes"}<label>发布状态<select bind:value={published} disabled={!!query}><option value="">全部状态</option><option value="true">已发布</option><option value="false">草稿</option></select></label>{/if}</div>
     <button disabled={busy}>{busy?"加载中…":"查找"}</button>
    </form>
    <div aria-busy={busy}>{#each list.items as item}<div class="item"><div class="grow"><p class="item-title">{item.title||"无标题笔记"}</p><p class="muted">{item.category||"未分类"} · {new Date(item.date).toLocaleDateString()} · {section==="notes"?"私人":item.publish?"已发布":"草稿"}</p></div><button onclick={()=>{itemId=item.id;editing=true;}}>编辑</button><button onclick={()=>remove(item)}>删除</button></div>{/each}
    {#if !busy&&!list.items.length}<p class="muted">这里还没有内容，可以新建一篇。</p>{/if}</div>
    <div class="row spread"><span class="muted">共 {list.total} 条 · 第 {page} 页</span><div class="row"><button disabled={page<=1||busy} onclick={()=>{page--;loadList();}}>上一页</button><button disabled={page>=list.total_pages||busy} onclick={()=>{page++;loadList();}}>下一页</button></div></div>
   </section>
  {/if}
 {:else if section==="wiki"||section==="chat"}
  {#key section}<Knowledge mode={section} />{/key}
 {:else if section==="settings"}<Settings />
 {:else if section==="ai-settings"}<AISettings />
 {:else if section==="appearance"}<ThemeSettings />
 {:else if section==="media"}<MediaLibrary />
 {:else if section==="maintenance"}<Maintenance />
 {:else if section==="setup"}<Onboarding onDone={()=>navigate("overview")} onWrite={()=>{void navigate("posts").then(()=>{itemId="";editing=true;});}} />{/if}
 {/if}
</div>
