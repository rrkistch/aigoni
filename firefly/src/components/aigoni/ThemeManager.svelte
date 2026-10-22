<script lang="ts">
 import {onMount} from "svelte";
 import {api,uploadTheme} from "./api";
 let state=$state<any>(null);let error=$state("");let message=$state("");let busy=$state(false);let progress=$state(0);let uploading=$state(false);let uploadId=$state("");let controller:AbortController|undefined;
 const pending=()=>state?.pending||["verifying","cancelling"].includes(state?.upload?.status);
 const label=(status:string)=>({uploading:"等待续传",verifying:"正在验证来源与文件",ready:"验证通过，可启用",failed:"验证失败",cancelled:"已取消",cancelling:"正在取消"}[status]||status);
 async function load(){state=await api("/theme");}
 onMount(()=>{void load().catch(e=>error=e.message);const timer=setInterval(()=>{if(pending())void load().catch(e=>error=e.message);},2000);return()=>{clearInterval(timer);controller?.abort();};});
 async function action(fn:()=>Promise<void>){busy=true;error="";message="";try{await fn();await load();}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function upload(e:Event){const input=e.currentTarget as HTMLInputElement;const file=input.files?.[0];if(!file)return;input.value="";uploading=true;uploadId="";progress=0;controller=new AbortController();await action(async()=>{try{await uploadTheme(file,p=>progress=p,controller?.signal,u=>{uploadId=u.id;state={...state,upload:u};});message="文件上传完成，正在验证；当前公开网站不变。";}catch(e){if((e as Error).name!=="AbortError")throw e;}});uploading=false;}
 async function activate(version:string,rollback=false){if(!confirm(rollback?"用上一主题重新构建当前内容和设置？不会恢复旧文章或已撤回页面。":"使用此主题构建当前内容和设置？构建成功后才切换公开网站。"))return;await action(async()=>{await api(rollback?"/theme/rollback":"/theme/activate","POST",{version});message="已排队构建，完成后才算主题上线。";});}
 async function cancel(){controller?.abort();await action(async()=>{if(state?.upload?.id)await api(`/theme/uploads/${state.upload.id}`,"DELETE");message="已取消上传或验证。";});}
</script>
<section class="card-base panel stack"><h1>主题更新</h1>
 <p class="muted">写文章、换壁纸、改音乐和导航，使用日常管理即可。只有升级主题代码时才需要上传主题包。</p>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if state}
  <div class="item"><span class="grow">当前主题</span><strong>{state.current||"原源码目录模式"}</strong></div>
  <p class="muted">服务器：{state.platform} · <a href={state.repository+"/releases"} target="_blank" rel="noreferrer">下载官方主题包 ↗</a></p>
  {#if !state.supported}<div class="notice">独立主题包首版仅支持 Linux amd64。当前机器仍可使用原有源码目录方式开发和构建。</div>{/if}
  <label>选择主题包<input type="file" accept=".tar.gz" disabled={!state.supported||busy||pending()} onchange={upload} /></label>
  <p class="muted">最大 1 GiB，以 8 MiB 分块上传。断线后重新选择同一文件可续传；只接受本项目正式 Release 的文件，验证失败不会执行代码。</p>
  {#if uploading}<progress max="100" value={progress} aria-label="主题上传进度"></progress><p role="status">已上传 {progress}%</p>{/if}
  {#if state.upload}
   <div class="stack"><h2>候选版本 {state.upload.tag}</h2><span class="pill">{label(state.upload.status)}</span>
    {#if state.upload.error}<p class="notice error">{state.upload.error}</p>{/if}
    {#if state.upload.manifest}<p class="muted">源码 {state.upload.manifest.source_commit.slice(0,12)} · Node {state.upload.manifest.node_major} · 配置兼容版本 {state.upload.manifest.settings}</p>{/if}
    <div class="row">{#if state.upload.status==="ready"}<button class="primary" disabled={busy||!!state.pending||state.current===state.upload.tag} onclick={()=>activate(state.upload.tag)}>构建并启用此主题</button>{/if}
    {#if ["uploading","verifying","cancelling"].includes(state.upload.status)||uploading}<button disabled={state.upload.status==="cancelling"||(uploading&&!uploadId)} onclick={cancel}>取消上传或验证</button>{/if}
    {#if state.upload.status==="failed"}<button disabled={busy} onclick={()=>action(async()=>{await api(`/theme/uploads/${state.upload.id}/complete`,"POST");})}>重新验证</button>{/if}{#if ["failed","cancelled"].includes(state.upload.status)}<button disabled={busy} onclick={cancel}>清除此候选</button>{/if}</div>
   </div>
  {/if}
  {#if state.pending}<div class="notice">正在使用 {state.pending} 构建。{state.build.status==="failed"?"构建失败，当前网站保持原版本；到概览重试构建，或取消切换。":"期间的内容变更会自动合并到新构建。"}</div><button disabled={busy} onclick={()=>action(async()=>{await api("/theme/cancel","POST",{});})}>取消切换，保留当前主题</button>{/if}
  <h2>回退与故障恢复</h2>
  {#if state.previous}<p>上一主题：{state.previous==="source"?"原源码目录（需仍然可用）":state.previous}</p><button disabled={busy||!!state.pending} onclick={()=>activate(state.previous,true)}>用上一主题重建当前网站</button>{:else}<p class="muted">首次安装后没有更早的独立主题。以后升级将保留一个可回退版本。</p>{/if}
  <p class="muted">失败时继续提供上一成功公开版本；已撤回内容仍按安全规则停供。若管理界面无法打开，使用 <a data-no-swup href={state.maintenance_url}>独立维护入口</a>。</p>
 {/if}
</section>
<style>progress{width:100%;accent-color:var(--primary)}strong{overflow-wrap:anywhere}</style>
