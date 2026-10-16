type AigoniSite = {posts_per_page?:number;name:string;description:string;author:string;base_url:string;logo:string;avatar:string;utc_offset:string;adminPath:string};
const raw=import.meta.env?.PUBLIC_AIGONI_SITE || (typeof process !== "undefined" ? process.env.PUBLIC_AIGONI_SITE : undefined);
export const aigoniSite:AigoniSite|null=raw ? JSON.parse(raw) : null;
