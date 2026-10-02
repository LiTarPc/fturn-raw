import {useEffect,useState} from 'react';
import {api} from '../api';
import type {Snapshot} from '../api';
export function useClient(){
 const [snapshot,setSnapshot]=useState<Snapshot>(),[busy,setBusy]=useState(false),[notice,setNotice]=useState('');
 useEffect(()=>{
  let alive=true;
  const refresh=()=>api.GetSnapshot().then(s=>{if(alive)setSnapshot(s)}).catch(e=>{if(alive)setNotice(String(e))});
  void refresh();const timer=setInterval(refresh,1000);
  const off=window.runtime?.EventsOn('snapshot',s=>{if(alive)setSnapshot(s)});
  return()=>{alive=false;clearInterval(timer);off?.()};
 },[]);
 const invoke=async(fn:()=>Promise<unknown>):Promise<boolean>=>{
  setBusy(true);setNotice('');
  try{await fn();setSnapshot(await api.GetSnapshot());return true}
  catch(e){setNotice(String(e));return false}
  finally{setBusy(false)}
 };
 const running=Boolean(snapshot&&['connecting','connected','reconnecting','stopping'].includes(snapshot.state));
 return {snapshot,busy,notice,setNotice,invoke,running};
}
