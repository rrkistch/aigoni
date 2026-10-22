<script lang="ts">
 import {api} from "./api";
 let {group,dirty=false}=$props<{group:string;dirty?:boolean}>();
 let busy=$state(false);let allowPrivate=$state(false);let error=$state("");let result=$state<any>(null);
 async function check(){busy=true;error="";result=null;try{result=await api(`/services/${group}/test`,"POST",{allow_private:allowPrivate});}catch(e){error=(e as Error).message;}finally{busy=false;}}
</script>
<section class="stack checks"><h2>连接检查与使用位置</h2>
 {#if group==="dynamic"}<p>选择本站动态后，在“动态”中编辑。选择 Memos 后，在 Memos 维护，并将要展示的内容设为公开；不要把私密访问令牌放入主题配置。</p>
 {:else if group==="comments"}<p>本站只连接评论系统。留言审核、账号与反垃圾设置仍在原服务管理；保存后还应在公开文章试发一条评论。</p>
 {:else if group==="analytics"}<p>统计报表在原服务查看。这里不执行统计脚本或发送访问事件；私人工作区和管理员预览不启用公开追踪、录屏。</p>
 {:else}<p>追番与收藏在原平台维护，本站只展示公开数据。静态模式需要重新生成网站；动态模式由公开页面读取源数据。</p>{/if}
 <p class="muted">仅检查已保存配置和明确列出的HTTP地址；可达不等于第三方权限、数据内容或统计事件已经正确。</p>
 <label class="allow"><input type="checkbox" bind:checked={allowPrivate} /><span>本次允许访问我明确配置的内网服务</span></label>
 <button type="button" disabled={busy||dirty} onclick={check}>{busy?"检查中…":"检查已保存的连接配置"}</button>{#if dirty}<small class="muted">先保存当前修改，再检查。</small>{/if}
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}
 {#if result}<div role="status" class="stack">{#each result.checks as item}<div class="item"><div class="grow"><strong>{item.label}</strong><p>{item.message}</p>{#if item.url}<small class="muted">{item.url}</small>{/if}</div><span class="pill">{item.status==="reachable"?"HTTP可达":item.status==="configured"?"配置已检查":"需要确认"}</span></div>{/each}<p class="muted">{result.scope}</p></div>{/if}
</section>
<style>.checks{margin-top:1rem;padding-top:1rem;border-top:1px solid var(--line-divider)}.allow{display:flex;gap:.6rem;align-items:center}.allow input{width:1.1rem;height:1.1rem;accent-color:var(--primary)}small{overflow-wrap:anywhere}</style>
