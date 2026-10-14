import {describe,it,expect} from 'vitest'
import {knowledgePackage} from './knowledge-package'
describe('merchant knowledge packaging',()=>{
 it('keeps document identity stable and versions content by its actual hash',async()=>{
  const read=async(text:string)=>JSON.parse(String((await knowledgePackage('shop-knowledge',[new File([text],'returns.md')],'s')).get('manifest')))
  const a=await read('first approved policy');const b=await read('second revised policy')
  expect(a.documents[0].document_id).toBe(b.documents[0].document_id)
  expect(a.documents[0].sha256).not.toBe(b.documents[0].sha256)
  expect(a.documents[0].disclosure_class).toBe('unclassified')
 })
 it('rejects ambiguous paths and unsupported files before upload',async()=>{
  await expect(knowledgePackage('shop',[new File(['x'],'x.pdf')],'s')).rejects.toThrow('支持')
  await expect(knowledgePackage('shop',[new File(['x'],'x.md'),new File(['y'],'x.md')],'s')).rejects.toThrow('唯一')
 })
})
