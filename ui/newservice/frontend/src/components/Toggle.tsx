export function Toggle({label,checked,disabled,onChange}:{label:string;checked:boolean;disabled:boolean;onChange:()=>void}){
 return <div className="setting-row"><span>{label}</span><button type="button" className={`toggle${checked?' on':''}`} role="switch" aria-label={label} aria-checked={checked} disabled={disabled} onClick={onChange}><span/></button></div>
}
