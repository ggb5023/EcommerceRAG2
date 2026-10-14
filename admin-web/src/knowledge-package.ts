export async function sha256(bytes: BufferSource): Promise<string> {
 return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),n=>n.toString(16).padStart(2,'0')).join('')
}
export async function knowledgePackage(source: string, files: File[], shop: string): Promise<FormData> {
 if(!/^[a-z][a-z0-9-]{0,63}$/.test(source))throw new Error('来源标识只能使用小写字母、数字和连字符。')
 if(!files.length||files.length>100||files.reduce((n,f)=>n+f.size,0)>8*1024*1024)throw new Error('请选择 1–100 个文件，总大小不超过 8 MiB。')
 const paths=new Set<string>();const documents=[]
 for(const file of files){
  if(paths.has(file.name)||!file.name||file.name.includes('/')||file.name.includes('\\'))throw new Error('文件名必须唯一且不包含路径。')
  paths.add(file.name)
  const ext=file.name.split('.').pop()?.toLowerCase();const format=({md:'markdown',markdown:'markdown',csv:'csv',docx:'docx'} as Record<string,string>)[ext??'']
  if(!format)throw new Error('首版支持 DOCX、Markdown 和 CSV。')
  const id=await sha256(new TextEncoder().encode(`${source}:${file.name}`))
  documents.push({document_id:`knowledge-${id.slice(0,32)}`,title:file.name,path:file.name,format,sha256:await sha256(await file.arrayBuffer()),disclosure_class:'unclassified'})
 }
 const form=new FormData();form.set('shop_id',shop);form.set('manifest',JSON.stringify({schema_version:1,source:{id:source,name:source},pipeline_version:'v2-customer-v1',documents}))
 for(const f of files){form.append('paths',f.name);form.append('files',f,f.name)}return form
}
