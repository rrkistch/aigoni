<script lang="ts">
 import {onMount} from "svelte";
 import {api} from "./api";
 let {onDone,onWrite}:{onDone:()=>void;onWrite:()=>void}=$props();
 const steps=["博客信息","选择模块","处理示例","开始写作"];
 let setup=$state<any>(null);let settings=$state<any>(null);let theme=$state<any>(null);let pages=$state<Record<string,boolean>>({});let step=$state(0);let mode=$state("clear");let busy=$state(false);let error=$state("");let message=$state("");
 async function load(){[setup,settings,theme]=await Promise.all([api("/onboarding"),api("/settings"),api("/theme-settings")]);step=setup.step;pages=JSON.parse(JSON.stringify(theme.effective.pages));mode=setup.sample_mode||"clear";}
 onMount(()=>{void load().catch(e=>error=e.message);});
 async function progress(next:number,done=false){await api("/onboarding","PATCH",{revision:setup.revision,step:next,completed:done});setup=await api("/onboarding");step=next;if(done)onDone();}
 async function next(){busy=true;error="";try{
  if(step===0)settings=await api("/settings","PATCH",{site:settings.site});
  if(step===1){await api("/theme-settings","PATCH",{revision:theme.revision,groups:{pages}});theme=await api("/theme-settings");}
  if(step===2){if(!confirm(mode==="clear"?"清空未修改的示例内容？个人内容和已经修改的配置会保留。":"导入原版示例动态、项目和关于页？只导入一次，不覆盖已有内容。")){busy=false;return;}const result=await api("/onboarding/samples","POST",{revision:setup.revision,mode,confirm:true});message=`已处理 ${result.changed} 项，保留 ${result.preserved} 项个人修改。`;setup=await api("/onboarding");}
  await progress(Math.min(step+1,3));
 }catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function avatar(e:Event,kind:string){const file=(e.currentTarget as HTMLInputElement).files?.[0];if(!file)return;busy=true;try{const data=new FormData();data.append("asset",file);const result=await api("/settings/assets/"+kind,"PUT",data);settings.site[kind==="avatar"?"author_avatar":"logo"]=result.path;message="图片已更新。";}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function finish(write=false){busy=true;try{await progress(3,true);if(write)onWrite();}catch(e){error=(e as Error).message;}finally{busy=false;}}
</script>
<section class="card-base panel stack"><div><p class="muted">从 Firefly 到你自己的博客</p><h1>建站引导</h1></div>
 <nav class="row" aria-label="建站步骤">{#each steps as title,index}<button disabled={busy} aria-current={step===index?"step":undefined} onclick={()=>step=index}>{index+1}. {title}</button>{/each}</nav>
 {#if error}<div class="notice error" role="alert">{error}<button onclick={()=>load().catch(e=>error=e.message)}>重新加载</button></div>{/if}{#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if settings&&setup}
 {#if step===0}<div class="stack"><h2>先告诉读者你是谁</h2><div class="fields"><label>博客名称<input bind:value={settings.site.name} /></label><label>作者名称<input bind:value={settings.site.author} /></label><label class="full">博客简介<textarea bind:value={settings.site.description}></textarea></label><label class="full">正式站点地址<input type="url" bind:value={settings.site.base_url} placeholder="https://blog.example.com" /></label></div><p class="muted">还没配域名可以稍后修改。正式地址会用于文章链接、RSS 和站点地图。</p><div class="row"><label class="upload">上传头像<input type="file" accept="image/*" disabled={busy} onchange={(e)=>avatar(e,"avatar")} /></label><label class="upload">上传 Logo<input type="file" accept="image/*" disabled={busy} onchange={(e)=>avatar(e,"logo")} /></label></div></div>
 {:else if step===1}<div class="stack"><h2>选择博客需要的页面</h2><p class="muted">保留 Firefly 原版布局。暂时不用的页面可以关闭，之后在外观设置中重新开启。</p><div class="fields">{#each Object.entries(theme.schema.pages.properties) as [key,field]}<label class="row"><input type="checkbox" bind:checked={pages[key]} />{(field as any).label}</label>{/each}</div></div>
 {:else if step===2}<div class="stack"><h2>怎么处理主题里的示例？</h2><label class="row"><input type="radio" name="samples" bind:group={mode} value="clear" />清空未修改的示例，开始自己的博客</label><label class="row"><input type="radio" name="samples" bind:group={mode} value="keep" />保留示例，并导入示例动态、项目和关于页</label><ul>{#each setup.samples as sample}<li>{sample}</li>{/each}</ul><p class="muted">不会删除壁纸等主题素材、私人笔记或已经编辑的个人内容。示例只导入一次，主题升级不会重复导入。</p></div>
 {:else}<div class="stack"><h2>开始写你的第一篇文章</h2><p>保存草稿 → 预览 → 公开发布 → 等待“已上线”。</p><p class="muted">以后换壁纸、歌单和导航，都在后台管理；修改后不需要更新 Docker 镜像。</p><button class="primary" disabled={busy} onclick={()=>finish(true)}>开始写文章</button><button disabled={busy} onclick={()=>finish()}>完成引导，返回概览</button></div>{/if}
 {#if step<3}<div class="row"><button class="primary" disabled={busy} onclick={next}>{busy?"处理中…":"保存并继续"}</button><button disabled={busy} onclick={()=>finish()}>稍后再设置</button></div>{/if}
 {/if}
</section>
