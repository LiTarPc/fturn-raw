import {useCallback,useEffect,useRef,useState} from 'react';
import {api} from '../api';
import type {Snapshot} from '../api';
export function useClient(){
 const [snapshot,setSnapshot]=useState<Snapshot>(),[pending,setPending]=useState(0),[connectionBusy,setConnectionBusy]=useState(false),[notice,setNotice]=useState('');
 const lifetime=useRef(0),alive=useRef(false),connectionPending=useRef(false);
 const accept=useCallback((next:Snapshot)=>{
  setSnapshot(previous=>!previous||next.revision>previous.revision?next:previous);
 },[]);
 useEffect(()=>{
  alive.current=true;
  const generation=++lifetime.current;
  let timer:ReturnType<typeof setTimeout>;
  const current=()=>alive.current&&lifetime.current===generation;
  const refresh=async()=>{
   try{const next=await api.GetSnapshot();if(current())accept(next)}
   catch(e){if(current())setNotice(String(e))}
   finally{if(current())timer=setTimeout(refresh,1000)}
  };
  const off=window.runtime?.EventsOn('snapshot',(next:Snapshot)=>{if(current())accept(next)});
  void refresh();
  return()=>{alive.current=false;clearTimeout(timer);off?.()};
 },[accept]);
 const invoke=async(fn:()=>Promise<unknown>,connection=false):Promise<boolean>=>{
  if(connection&&connectionPending.current)return false;
  const generation=lifetime.current;
  const current=()=>alive.current&&lifetime.current===generation;
  if(connection){connectionPending.current=true;setConnectionBusy(true)}
  setPending(n=>n+1);setNotice('');
  try{await fn();const next=await api.GetSnapshot();if(current())accept(next);return true}
  catch(e){if(current())setNotice(String(e));return false}
  finally{
   if(connection)connectionPending.current=false;
   if(current()){setPending(n=>n-1);if(connection)setConnectionBusy(false)}
  }
 };
 const running=Boolean(snapshot&&['connecting','connected','reconnecting','stopping'].includes(snapshot.state));
 return {snapshot,busy:pending>0,connectionBusy,notice,setNotice,invoke,
  invokeConnection:(fn:()=>Promise<unknown>)=>invoke(fn,true),running};
}
