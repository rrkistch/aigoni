<script lang="ts">
 import {onMount} from "svelte";
 import {api} from "./api";
 let settings=$state<any>(null);let error=$state("");let message=$state("");let busy=$state(false);let status=$state<any>(null);
 async function load(){settings=await api("/settings");status=await api("/wiki/status");}
 onMount(()=>{load().catch(e=>error=e.message);});
 async function save(){
  busy=true;error="";
  try{settings=await api("/settings","PATCH",{site:settings.site,pagination:settings.pagination,paths:settings.paths});message="设置已保存，公开站点将自动重建。";}
  catch(e){error=(e as Error).message;}finally{busy=false;}
 }
 async function upload(e:Event,kind:string){
  const file=(e.currentTarget as HTMLInputElement).files?.[0];if(!file)return;
  const data=new FormData();data.append("asset",file);
  try{await api("/settings/assets/"+kind,"PUT",data);await load();message="图片已更新";}
  catch(e){error=(e as Error).message;}
 }
 async function remove(kind:string){if(!confirm("删除当前站点图片？"))return;try{await api("/settings/assets/"+kind,"DELETE");await load();}catch(e){error=(e as Error).message;}}
 async function cleanup(){if(!confirm("清理 Wiki 备份目录？此操作不能撤销。"))return;try{message=(await api("/wiki/backups","DELETE")).message;}catch(e){error=(e as Error).message;}}
</script>
<section class="card-base panel stack">
 <h1>站点设置</h1>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if settings}
 <form class="stack" onsubmit={(e)=>{e.preventDefault();save();}}>
  <div class="fields">
   <label>站点名称<input bind:value={settings.site.name} required /></label>
   <label>作者<input bind:value={settings.site.author} /></label>
   <label class="full">站点描述<textarea bind:value={settings.site.description}></textarea></label>
   <label>站点地址<input type="url" bind:value={settings.site.base_url} required /></label>
   <label>时区偏移<input bind:value={settings.site.utc_offset} placeholder="+08:00" /></label>
   <label>列表每页数量<input type="number" min="1" bind:value={settings.pagination.posts_per_page} /></label>
   <label>首页文章数量<input type="number" min="1" bind:value={settings.pagination.home_posts_count} /></label>
  </div>
  <details><summary>内容存储路径</summary><p class="muted">相对于 aigoni 运行目录。修改路径不会移动已有内容。</p><div class="fields">{#each Object.keys(settings.paths) as key}<label>{key}<input bind:value={settings.paths[key]} /></label>{/each}</div></details>
  <button class="primary" disabled={busy}>{busy?"保存中…":"保存设置"}</button>
 </form>
 <h2>站点图片</h2><div class="row">{#each ["logo","avatar"] as kind}<div class="stack"><label class="upload">{kind==="logo"?"站点 Logo":"作者头像"}<input type="file" accept="image/*" onchange={(e)=>upload(e,kind)} /></label><button onclick={()=>remove(kind)}>移除{kind==="logo"?" Logo":"头像"}</button></div>{/each}</div>
 <h2>知识库维护</h2>
 {#if status}<details><summary>运行状态</summary><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{JSON.stringify(status,null,2)}</pre></details>{/if}
 <button onclick={cleanup}>清理 Wiki 备份</button>
 {/if}
</section>
