export function bytes(n:number){if(n<1024)return `${n} Б`;if(n<1048576)return `${(n/1024).toFixed(1)} КБ`;return `${(n/1048576).toFixed(1)} МБ`}
export function duration(seconds:number){const n=Math.max(0,Math.floor(seconds));return [Math.floor(n/3600),Math.floor(n/60)%60,n%60].map(v=>String(v).padStart(2,'0')).join(':')}
