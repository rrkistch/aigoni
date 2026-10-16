<script lang="ts">
 import {onMount} from "svelte";
 import {api} from "./api";
 type Provider={id:string;name:string;base_url:string;model:string;allow_private:boolean;has_key?:boolean;api_key?:string};
 let state=$state<{revision:number;default_provider:string;source:string;providers:Provider[]}|null>(null);
 let form=$state<Provider>({id:"",name:"",base_url:"",model:"",allow_private:false,api_key:""});
 let editing=$state(false);let models=$state<string[]>([]);let filter=$state("");let busy=$state(false);let message=$state("");let error=$state("");
 async function load(){state=await api("/ai/providers");}
 onMount(()=>{load().catch(e=>error=e.message);});
 const clean=(p:Provider)=>({id:p.id,name:p.name,base_url:p.base_url,model:p.model,allow_private:p.allow_private,...(p.api_key?{api_key:p.api_key}:{})});
 function edit(p?:Provider){form=p?{...p,api_key:""}:{id:"",name:"",base_url:"",model:"",allow_private:false,api_key:""};editing=true;models=[];filter="";message="";error="";}
 async function action(fn:()=>Promise<void>){busy=true;error="";message="";try{await fn();}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function save(){await action(async()=>{
  if(!state)return;
  const providers=state.providers.filter(p=>p.id!==form.id).map(clean);providers.push(clean(form));
  state=await api("/ai/providers","PATCH",{revision:state.revision,default_provider:state.default_provider,providers});
  const saved=state!.providers.find(p=>p.id===form.id)||state!.providers.at(-1)!;form={...saved,api_key:""};
  message="连接已保存。可以获取模型列表，再选择知识库默认模型。";
 });}
 async function discover(){await action(async()=>{models=(await api(`/ai/providers/${form.id}/models`,"POST")).models;message=models.length?`获取到 ${models.length} 个模型。选择后请保存。`:"服务未返回模型，可手动填写模型 ID。";});}
 async function selectDefault(id:string){await action(async()=>{if(!state)return;state=await api("/ai/providers","PATCH",{revision:state.revision,default_provider:id,providers:state.providers.map(clean)});message="默认连接已更新，新任务立即生效；进行中的任务不受影响。";});}
 async function remove(p:Provider){if(!confirm(`删除连接「${p.name}」？正在进行的任务不会被取消。`))return;await action(async()=>{if(!state)return;state=await api("/ai/providers","PATCH",{revision:state.revision,default_provider:state.default_provider===p.id?"":state.default_provider,providers:state.providers.filter(v=>v.id!==p.id).map(clean)});if(form.id===p.id)editing=false;message="连接已删除。";});}
 async function test(){if(!confirm("将发送一次小型流式与工具调用测试，可能产生少量模型费用。不会读取或修改知识库。继续？"))return;await action(async()=>{const result=await api(`/ai/providers/${form.id}/test`,"POST",{model:form.model,confirm_cost:true});message=result.passed?"测试通过：流式响应与工具调用可用。":`测试未完全通过：流式 ${result.streaming?"通过":"未通过"}，工具调用 ${result.tool_calling?"通过":"未通过"}，响应完成 ${result.complete?"是":"否"}。`;});}
</script>
<section class="card-base panel stack">
 <div class="row spread"><div><h1>AI 服务</h1><p class="muted">连接你的模型服务，让知识库使用你选择的模型。</p></div><button class="primary" disabled={busy} onclick={()=>edit()}>＋ 添加服务</button></div>
 {#if error}<div class="notice error" role="alert">{error}<button disabled={busy} onclick={()=>action(async()=>{await load();editing=false;message="已重新加载，请重新编辑。";})}>重新加载</button></div>{/if}
 {#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if state}
 <label>知识库默认连接<select aria-label="知识库默认连接" disabled={busy} value={state.default_provider} onchange={(e)=>selectDefault(e.currentTarget.value)}><option value="">使用服务器环境配置</option>{#each state.providers as p}<option value={p.id} disabled={!p.has_key||!p.model}>{p.name} · {p.model||"尚未选择模型"}</option>{/each}</select></label>
 <p class="muted">当前来源：{state.source==="environment"?"服务器环境配置":"后台配置"}。更改后无需重启，不会自动切换到其他收费模型。</p>
 {#each state.providers as p}<div class="item"><div class="grow"><strong>{p.name}</strong><p class="muted">{p.model||"未选择模型"} · {p.has_key?"密钥已保存":"未配置密钥"}</p></div><button disabled={busy} onclick={()=>edit(p)}>编辑</button><button disabled={busy} onclick={()=>remove(p)}>删除</button></div>{/each}
 {#if !state.providers.length}<p class="muted">还没有后台连接。现有环境配置继续可用。</p>{/if}
 {/if}
 {#if editing}
 <form class="stack" onsubmit={(e)=>{e.preventDefault();save();}}>
 <h2>{form.id?"编辑连接":"添加连接"}</h2>
 <div class="fields">
 <label>服务名称<input required maxlength="100" bind:value={form.name} placeholder="例如：我的模型服务" /></label>
 <label>接口类型<select disabled><option>OpenAI 兼容</option></select></label>
 <label class="full">API 基础地址<input aria-label="API 基础地址" type="url" required bind:value={form.base_url} placeholder="https://你的服务地址/v1" /><small>填写服务商提供的完整基础地址；不会自动追加 /v1。</small></label>
 <label class="full">API Key<input aria-label="API Key" type="password" autocomplete="new-password" bind:value={form.api_key} placeholder={form.has_key?"已保存；留空保持原密钥":"输入密钥"} /><small>更换 API 地址后需重新填写密钥。已保存的密钥不会返回浏览器。</small></label>
 </div>
 <label class="row"><input type="checkbox" bind:checked={form.allow_private} />允许此连接访问内网／本机模型服务</label>
 <button class="primary" disabled={busy}>{busy?"处理中…":"保存连接"}</button>
 </form>
 <div class="stack"><h2>选择模型</h2><button disabled={busy||!form.id} onclick={discover}>获取模型列表</button>
 {#if !form.id}<p class="muted">先保存连接，再获取模型。</p>{/if}
 {#if models.length}<label>筛选模型<input type="search" bind:value={filter} placeholder="搜索模型名称" /></label><label>可用模型<select aria-label="可用模型" bind:value={form.model}><option value="">请选择</option>{#each models.filter(m=>m.toLowerCase().includes(filter.toLowerCase())) as model}<option value={model}>{model}</option>{/each}</select></label>{/if}
 <label>模型 ID<input bind:value={form.model} maxlength="200" placeholder="可从列表选择，也可手动填写" /></label>
 <p class="muted">列表只说明服务提供了该模型，不代表它一定支持知识库所需的流式和工具调用。</p>
 <div class="row"><button disabled={busy||!form.id} onclick={save}>保存模型选择</button><button disabled={busy||!form.id||!form.model} onclick={test}>测试模型（可能产生费用）</button><button disabled={busy} onclick={()=>{editing=false;}}>收起</button></div></div>
 {/if}
</section>
