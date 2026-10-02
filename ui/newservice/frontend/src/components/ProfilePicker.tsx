import {useEffect,useRef,useState} from 'react';
import {IconServer,IconChevronDown,IconChevronUp,IconSettings2,IconTrash,IconX,IconCheck} from '@tabler/icons-react';
import type {SavedProfile} from '../api';
type Props={profiles:SavedProfile[];activeId:string;disabled:boolean;onSelect:(id:string)=>void;onEdit:(p:SavedProfile)=>void;onDelete:(id:string)=>void};
export function ProfilePicker({profiles,activeId,disabled,onSelect,onEdit,onDelete}:Props){
 const [open,setOpen]=useState(false),[deleting,setDeleting]=useState('');
 const root=useRef<HTMLDivElement>(null);
 const selected=profiles.find(p=>p.id===activeId);
 useEffect(()=>{const outside=(e:PointerEvent)=>{if(!root.current?.contains(e.target as Node)){setOpen(false);setDeleting('')}};const escape=(e:KeyboardEvent)=>{if(e.key==='Escape'){setOpen(false);setDeleting('')}};document.addEventListener('pointerdown',outside);document.addEventListener('keydown',escape);return()=>{document.removeEventListener('pointerdown',outside);document.removeEventListener('keydown',escape)}},[]);
 useEffect(()=>{if(disabled){setOpen(false);setDeleting('')}},[disabled]);
 return <div className="profile-menu" ref={root}>
  {open&&<div className="profile-menu-list" aria-label="Серверы">{profiles.map(p=><div key={p.id} className={`profile-menu-row${p.id===activeId?' selected':''}`}>
   <button className="profile-select" disabled={disabled||deleting===p.id} onClick={()=>{onSelect(p.id);setOpen(false)}} title={p.profile.Server}><IconServer size={17}/><span>{deleting===p.id?'Удалить профиль?':p.name}</span></button>
   {deleting===p.id?<><button className="icon-button danger" title="Подтвердить удаление" aria-label={`Подтвердить удаление ${p.name}`} onClick={()=>{onDelete(p.id);setDeleting('')}}><IconCheck size={16}/></button><button className="icon-button" title="Отмена" aria-label="Отменить удаление" onClick={()=>setDeleting('')}><IconX size={16}/></button></>:<><button className="icon-button" disabled={disabled} title={`Редактировать ${p.name}`} aria-label={`Редактировать ${p.name}`} onClick={()=>{onEdit(p);setOpen(false)}}><IconSettings2 size={16}/></button><button className="icon-button" disabled={disabled||profiles.length<=1} title={`Удалить ${p.name}`} aria-label={`Удалить ${p.name}`} onClick={()=>setDeleting(p.id)}><IconTrash size={16}/></button></>}
  </div>)}</div>}
  <div className="status-server selected-profile">
   <button className="profile-select" disabled={disabled||!profiles.length} aria-label="Выбрать сервер" aria-expanded={open} onClick={()=>setOpen(x=>!x)}><IconServer size={20}/><span className="selected-profile-text"><strong>{selected?.name??'Добавь профиль'}</strong><small>{selected?.profile.Server??'Кнопка + сверху'}</small></span></button>
   {selected&&<button className="icon-button" disabled={disabled} title="Редактировать профиль" aria-label="Редактировать профиль" onClick={()=>onEdit(selected)}><IconSettings2 size={17}/></button>}
   <button className="icon-button" disabled={disabled||!profiles.length} title="Список серверов" aria-label="Список серверов" aria-expanded={open} onClick={()=>setOpen(x=>!x)}>{open?<IconChevronUp size={17}/>:<IconChevronDown size={17}/>}</button>
  </div>
 </div>
}
