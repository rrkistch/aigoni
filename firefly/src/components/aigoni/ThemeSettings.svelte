<script lang="ts">
 import {onMount} from "svelte";
 import {api} from "./api";
 import ServiceChecks from "./ServiceChecks.svelte";
 import ConfigFields from "./ConfigFields.svelte";
 let {section="appearance"}=$props<{section?:"appearance"|"modules"|"services"}>();
 const moduleGroups=["music","announcement","friends","booknav","sponsor","gallery"];
 const serviceGroups=["comments","analytics","dynamic","collections"];
 const allowed=(name:string)=>section==="modules"?moduleGroups.includes(name):section==="services"?serviceGroups.includes(name):!moduleGroups.includes(name)&&!serviceGroups.includes(name);
 const heading=()=>section==="modules"?"内容模块":section==="services"?"服务接入":"外观设置";
 let preview=$state<any>(null);let previewTimer:ReturnType<typeof setInterval>|undefined;let history=$state<any[]>([]);let data=$state<any>(null);let selected=$state("appearance");let value=$state<any>({});let dirty=$state(false);let busy=$state(false);let error=$state("");let message=$state("");
 async function load(){data=await api("/theme-settings");if(!allowed(selected))selected=Object.keys(data.schema).find(allowed)||"appearance";value=JSON.parse(JSON.stringify(data.effective[selected]));dirty=false;}
 onMount(()=>{void load().catch(e=>error=e.message);return()=>{if(previewTimer)clearInterval(previewTimer);};});
 function select(name:string){if(dirty&&!confirm("放弃当前未保存修改？"))return;selected=name;value=JSON.parse(JSON.stringify(data.effective[name]));dirty=false;message="";error="";}
 async function save(reset=false){if(reset&&!confirm("恢复这一组的主题默认值？其他设置不会改变。"))return;busy=true;error="";try{const result=await api("/theme-settings","PATCH",{revision:data.revision,groups:{[selected]:reset?null:value}});await load();message=result.build_status==="failed"?"设置已保存，但排队构建失败，请到概览重试。":"设置已保存，请到概览查看网站上线状态。";}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function showHistory(){try{history=(await api("/settings-history")).items.reverse();}catch(e){error=(e as Error).message;}}
 async function restore(revision:number){if(!confirm(`恢复外观与模块设置到版本 ${revision}？不会回退文章和私人数据。`))return;busy=true;try{await api(`/settings-history/${revision}/restore`,"POST",{revision:data.revision});await load();await showHistory();message="设置已恢复，等待网站重新上线。";}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function makePreview(){busy=true;error="";try{preview=await api("/previews","POST",{groups:{[selected]:value}});if(previewTimer)clearInterval(previewTimer);previewTimer=setInterval(async()=>{try{preview=await api("/previews/"+preview.id);if(["ready","failed"].includes(preview.status))clearInterval(previewTimer);}catch(e){error=(e as Error).message;clearInterval(previewTimer);}},2000);}catch(e){error=(e as Error).message;}finally{busy=false;}}
</script>
<section class="card-base panel stack">
 <div><h1>{heading()}</h1><p class="muted">使用 Firefly 原版组件。保存后自动生成网站，不需要更新镜像。</p></div>
 {#if error}<div class="notice error" role="alert">{error}<button onclick={()=>load().catch(e=>error=e.message)}>重新加载（放弃修改）</button></div>{/if}
 {#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if data}
 <label>选择要管理的区域<select aria-label="选择要管理的区域" value={selected} disabled={busy} onchange={(e)=>select(e.currentTarget.value)}>{#each Object.entries(data.schema).filter(([name])=>allowed(name)) as [name,spec]}<option value={name}>{(spec as any).label}</option>{/each}</select></label>
 <p class="muted">修改的是全站默认值，不会清除访客自己选择的显示偏好。头像和 Logo 在“设置”中管理。</p>
 {#key selected}<form class="stack" onsubmit={(e)=>{e.preventDefault();save();}}><ConfigFields schema={data.schema[selected]} {value} onchange={(v)=>{value=v;dirty=true;}} /><div class="row"><button class="primary" disabled={busy||!dirty}>{busy?"保存中…":"保存并生成网站"}</button><button type="button" disabled={busy||preview?.status==="queued"||preview?.status==="building"} onclick={makePreview}>生成登录态预览</button><button type="button" disabled={busy} onclick={()=>save(true)}>恢复本组默认</button><span class="muted">{dirty?"有未保存修改":"已保存"}</span></div></form>{/key}
 {#if ["comments","analytics","dynamic","collections"].includes(selected)}{#key selected}<ServiceChecks group={selected} {dirty} />{/key}{/if}
 {/if}
  {#if preview}<div class="notice" role="status">{#if preview.status==="ready"}<a href={preview.url} target="_blank" rel="noreferrer" data-no-swup>打开真实页面预览 ↗</a><span> · 仅登录可见，24 小时有效</span>{:else if preview.status==="failed"}{preview.error}{:else}正在生成预览，不会改变公开网站…{/if}</div>{/if}
 <details><summary onclick={showHistory}>设置历史（最近 20 份）</summary><div class="stack">{#each history as item}<div class="row spread"><span>版本 {item.revision} · {new Date(item.saved_at).toLocaleString()}</span><button disabled={busy} onclick={()=>restore(item.revision)}>恢复此版本</button></div>{/each}{#if !history.length}<p class="muted">暂无历史设置。</p>{/if}</div></details>
</section>
